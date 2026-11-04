package cache

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// entryOverhead is the fixed per-entry accounting charge added to every
// stored entry, on top of key and value bytes. It approximates the map entry,
// LRU list element, and bookkeeping fields an entry costs, NOT its exact heap
// footprint. See MemoryConfig.MaxBytes for what accounting measures.
const entryOverhead = 64

// ErrInvalidConfig reports a MemoryConfig rejected by NewMemory: a capacity
// limit that is zero, negative, mutually inconsistent, or too small to hold
// even one minimal entry. It is a construction-time caller bug, like
// ErrInvalidPolicy for policies.
var ErrInvalidConfig = errors.New("cache: invalid memory configuration")

// MemoryConfig bounds a Memory store. Every field counts ACCOUNTED bytes or
// entries (see below); none of them is a strict process-RSS or Go-heap bound.
//
// All capacity fields are required positive except MaxEntryBytes, which may be
// zero to mean "no separate per-entry limit; MaxBytes still applies".
// There are no implicit defaults: a zero MaxEntries or MaxBytes is an error,
// not an unlimited cache.
type MemoryConfig struct {
	// MaxEntries is the maximum number of live entries. Must be > 0.
	MaxEntries int

	// MaxBytes is the maximum total accounted bytes. Must be > 0 and large
	// enough to hold one minimal entry (a 1-byte key with an empty value,
	// i.e. entryOverhead+1). Accounting charges, per entry:
	//
	//	len(key) + len(value) + entryOverhead
	//
	// It does NOT charge map growth slack, allocator headers, or goroutine
	// stacks, so real heap use exceeds accounted use by an implementation-
	// dependent factor. The bound is exact only against the formula above.
	MaxBytes int64

	// MaxEntryBytes is the maximum accounted size of a single entry using
	// the same formula as MaxBytes. Zero disables the separate limit; the
	// total bound still applies. Must not exceed MaxBytes (an entry that
	// could never fit is a configuration bug). A larger entry is rejected
	// by Set with an error wrapping ErrEntryTooLarge and stores nothing.
	MaxEntryBytes int64
}

// validate reports whether c is a usable configuration.
func (c MemoryConfig) validate() error {
	if c.MaxEntries <= 0 {
		return fmt.Errorf("%w: MaxEntries must be positive, got %d", ErrInvalidConfig, c.MaxEntries)
	}
	if c.MaxBytes <= 0 {
		return fmt.Errorf("%w: MaxBytes must be positive, got %d", ErrInvalidConfig, c.MaxBytes)
	}
	if c.MaxEntryBytes < 0 {
		return fmt.Errorf("%w: MaxEntryBytes must be zero or positive, got %d", ErrInvalidConfig, c.MaxEntryBytes)
	}
	if c.MaxEntryBytes > c.MaxBytes {
		return fmt.Errorf("%w: MaxEntryBytes %d exceeds MaxBytes %d", ErrInvalidConfig, c.MaxEntryBytes, c.MaxBytes)
	}
	// The smallest storable entry is a 1-byte key (keys are non-empty) with
	// an empty value. A store that cannot hold even that can satisfy no Set.
	if minEntry := int64(entryOverhead + 1); c.MaxBytes < minEntry {
		return fmt.Errorf("%w: MaxBytes %d cannot hold one minimal entry (%d accounted bytes)", ErrInvalidConfig, c.MaxBytes, minEntry)
	}
	return nil
}

// memoryEntry is the stored representation. size is the entry's accounted
// size (len(key)+len(value)+entryOverhead), computed once at admission.
type memoryEntry struct {
	key       Key
	value     []byte // store-owned; never aliased with caller slices
	size      int64
	expiresAt time.Time
}

// Memory is the E02 bounded in-memory TTL cache implementing Store.
//
// Behavior summary (full normative text lives on the Store methods):
//
//   - Lookup is O(1) expected via map; eviction order is an intrusive
//     doubly-linked LRU list. Get and Set both refresh recency.
//   - Expiry is lazy: Get treats an expired entry as a miss and removes it;
//     a Set under capacity pressure first sweeps expired entries (O(n)) and
//     only then evicts least-recently-used LIVE entries. Expired entries
//     therefore hold accounted bytes until something touches them — there
//     is no background sweeper, and physical reclamation happens whenever
//     the Go collector runs, not at the TTL boundary.
//   - TTL is fixed, not sliding: reads never extend a deadline; a Set on an
//     existing key replaces both value and deadline.
//   - All operations take one mutex, so individual operations are atomic and
//     visibility follows lock order: an operation started after another
//     returned observes its effect. This is per-process consistency only.
//   - Close releases every entry. It is idempotent and safe to call
//     concurrently with operations; see Close.
//
// Memory has no background goroutines, no filesystem access, and no global
// state; each instance is independent. Construct with NewMemory.
type Memory struct {
	mu      sync.Mutex
	cfg     MemoryConfig
	entries map[Key]*list.Element // element.Value is *memoryEntry
	lru     *list.List            // front = most recently used, back = victim
	usage   int64                 // total accounted bytes of live entries
	now     func() time.Time
	closed  bool
}

// Compile-time proof that Memory satisfies the E01 contract.
var _ Store = (*Memory)(nil)

// NewMemory constructs a bounded in-memory cache. The configuration is
// validated up front; no partial store is returned on error. Construction
// starts no goroutines and reads no environment. Production uses time.Now;
// tests inject a clock through newMemory (unexported to keep the public API
// minimal).
func NewMemory(config MemoryConfig) (*Memory, error) {
	return newMemory(config, time.Now)
}

func newMemory(config MemoryConfig, now func() time.Time) (*Memory, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	if now == nil {
		now = time.Now
	}
	return &Memory{
		cfg:     config,
		entries: make(map[Key]*list.Element, config.MaxEntries),
		lru:     list.New(),
		now:     now,
	}, nil
}

// Get implements Store. Error precedence: a nil context first, then an
// already-canceled/expired context, then key validation, then closure — the
// same order the E01 contract tests established. A hit moves the entry to
// the most-recent end of the LRU list and returns a private copy of the
// bytes; a miss (never written, deleted, or expired) returns (zero, false,
// nil). An expired entry found at read time is removed (lazy reclamation)
// and reported as a miss, never as stale data.
func (m *Memory) Get(ctx context.Context, key Key) (Entry, bool, error) {
	if ctx == nil {
		return Entry{}, false, ErrNilContext
	}
	if err := ctx.Err(); err != nil {
		return Entry{}, false, err
	}
	if err := key.Validate(); err != nil {
		return Entry{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return Entry{}, false, ErrClosed
	}
	elem, ok := m.entries[key]
	if !ok {
		return Entry{}, false, nil
	}
	e := elem.Value.(*memoryEntry)
	if m.expired(e) {
		m.remove(elem)
		return Entry{}, false, nil
	}
	m.lru.MoveToFront(elem)
	out := make([]byte, len(e.value))
	copy(out, e.value)
	return Entry{Value: out}, true, nil
}

// Set implements Store. Validation and size checks all happen BEFORE any
// stored entry is touched, so a rejected Set (bad key, bad policy, oversized
// value, closed store, canceled context) leaves the cache byte-for-byte
// unchanged — including the previous entry under the same key. Only then,
// in one critical section: the old entry for key (if any) is replaced, then
// entries are evicted until the new usage fits both limits, then the new
// entry is published. Eviction first removes expired entries wholesale, and
// only then least-recently-used live entries, never the key being Set. The
// write's linearization point is the mutex release; cancellation observed
// after that cannot undo it, and cancellation checked before it prevented
// every mutation.
func (m *Memory) Set(ctx context.Context, key Key, entry Entry, policy Policy) error {
	if ctx == nil {
		return ErrNilContext
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := key.Validate(); err != nil {
		return err
	}
	if err := policy.Validate(); err != nil {
		return err
	}
	newSize := int64(len(key)) + int64(len(entry.Value)) + entryOverhead
	if m.cfg.MaxEntryBytes != 0 && newSize > m.cfg.MaxEntryBytes {
		return fmt.Errorf("%w: entry accounts for %d bytes, per-entry limit is %d", ErrEntryTooLarge, newSize, m.cfg.MaxEntryBytes)
	}
	if newSize > m.cfg.MaxBytes {
		return fmt.Errorf("%w: entry accounts for %d bytes, total capacity is %d", ErrEntryTooLarge, newSize, m.cfg.MaxBytes)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	// Reclaim expired entries before deciding on eviction: an expired entry
	// is dead weight and must not cost a live one its place. O(n) in entries,
	// charged only to Sets that need room.
	m.reclaimExpired()

	// Replace any existing entry for key first (expired or live): its
	// accounted size frees immediately.
	if elem, ok := m.entries[key]; ok {
		m.remove(elem)
	}
	// Evict LRU live victims until the new entry fits. newSize <= MaxBytes
	// was already proven, and every eviction strictly decreases usage, so
	// the loop terminates.
	for int64(len(m.entries))+1 > int64(m.cfg.MaxEntries) || m.usage+newSize > m.cfg.MaxBytes {
		victim := m.lru.Back()
		if victim == nil {
			// Empty store: the prechecks guarantee the entry fits alone.
			break
		}
		m.remove(victim)
	}

	value := make([]byte, len(entry.Value))
	copy(value, entry.Value)
	e := &memoryEntry{
		key:       key,
		value:     value,
		size:      newSize,
		expiresAt: m.now().Add(policy.TTL),
	}
	m.entries[key] = m.lru.PushFront(e)
	m.usage += newSize
	return nil
}

// Delete implements Store. Removing a missing key — including an expired
// one — is a successful no-op, so exact-key invalidation is idempotent.
func (m *Memory) Delete(ctx context.Context, key Key) error {
	if ctx == nil {
		return ErrNilContext
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := key.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	if elem, ok := m.entries[key]; ok {
		m.remove(elem)
	}
	return nil
}

// Close releases every stored entry and rejects all later operations with
// ErrClosed. It is idempotent and safe to call concurrently with operations
// or with itself. A caller holding bytes from an earlier Get keeps them:
// Close makes no attempt to erase copies already handed out, and the Go
// collector, not Close, decides when the underlying memory is physically
// reused — values are not zeroed on release. There is no reopen; construct
// a new Memory instead. Close always returns nil.
func (m *Memory) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	m.entries = nil
	m.lru = nil
	m.usage = 0
	return nil
}

// expired reports whether e's lifetime is over. The served interval is
// [stored, stored+TTL): at exactly stored+TTL the entry is expired. Callers
// hold m.mu.
func (m *Memory) expired(e *memoryEntry) bool {
	return !m.now().Before(e.expiresAt)
}

// remove unlinks an entry and releases its accounting. Callers hold m.mu.
func (m *Memory) remove(elem *list.Element) {
	e := m.lru.Remove(elem).(*memoryEntry)
	delete(m.entries, e.key)
	m.usage -= e.size
}

// reclaimExpired removes every expired entry in one sweep. Callers hold m.mu.
func (m *Memory) reclaimExpired() {
	for elem := m.lru.Front(); elem != nil; {
		next := elem.Next()
		if m.expired(elem.Value.(*memoryEntry)) {
			m.remove(elem)
		}
		elem = next
	}
}

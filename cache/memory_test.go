package cache

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeClock is a manually advanced clock for deterministic TTL tests. advance
// moves the monotonic reading forward; no test in this package sleeps.
type fakeClock struct{ now time.Time }

func newFakeClock() *fakeClock { return &fakeClock{now: time.Now()} }

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) advance(d time.Duration) { c.now = c.now.Add(d) }

// mustKey builds a key or fails the test.
func mustKey(t *testing.T, namespace string, components ...string) Key {
	t.Helper()
	k, err := NewKey(namespace, components...)
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}
	return k
}

func smallCfg() MemoryConfig {
	return MemoryConfig{MaxEntries: 1024, MaxBytes: 1 << 20, MaxEntryBytes: 1 << 18}
}

func TestNewMemoryConfigValidation(t *testing.T) {
	minEntry := int64(entryOverhead + 1)
	cases := []struct {
		name string
		cfg  MemoryConfig
		ok   bool
	}{
		{"valid", MemoryConfig{MaxEntries: 10, MaxBytes: 1 << 20}, true},
		{"valid with per-entry limit", MemoryConfig{MaxEntries: 10, MaxBytes: 1 << 20, MaxEntryBytes: 4096}, true},
		{"zero entries", MemoryConfig{MaxEntries: 0, MaxBytes: 1 << 20}, false},
		{"negative entries", MemoryConfig{MaxEntries: -1, MaxBytes: 1 << 20}, false},
		{"zero bytes", MemoryConfig{MaxEntries: 10, MaxBytes: 0}, false},
		{"negative bytes", MemoryConfig{MaxEntries: 10, MaxBytes: -1}, false},
		{"negative per-entry", MemoryConfig{MaxEntries: 10, MaxBytes: 1 << 20, MaxEntryBytes: -1}, false},
		{"per-entry exceeds total", MemoryConfig{MaxEntries: 10, MaxBytes: 100, MaxEntryBytes: 200}, false},
		{"bytes below one minimal entry", MemoryConfig{MaxEntries: 10, MaxBytes: minEntry - 1}, false},
		{"exactly one minimal entry", MemoryConfig{MaxEntries: 1, MaxBytes: minEntry}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := NewMemory(tc.cfg)
			if tc.ok && err != nil {
				t.Fatalf("valid config rejected: %v", err)
			}
			if !tc.ok {
				if err == nil {
					t.Fatalf("invalid config accepted")
				}
				if !errors.Is(err, ErrInvalidConfig) {
					t.Fatalf("want ErrInvalidConfig, got %v", err)
				}
			}
			_ = m
		})
	}
}

func TestMemoryCopyInOut(t *testing.T) {
	m, _ := newMemory(smallCfg(), time.Now)
	t.Cleanup(func() { _ = m.Close() })
	ctx := context.Background()
	key := mustKey(t, "ns", "k")

	// Copy-in.
	buf := []byte("hello")
	if err := m.Set(ctx, key, Entry{Value: buf}, Policy{TTL: time.Minute}); err != nil {
		t.Fatal(err)
	}
	buf[0] = 'X'
	e, ok, err := m.Get(ctx, key)
	if err != nil || !ok || string(e.Value) != "hello" {
		t.Fatalf("copy-in violated: %q ok=%v err=%v", e.Value, ok, err)
	}
	// Copy-out.
	e.Value[0] = 'Y'
	e2, ok, _ := m.Get(ctx, key)
	if !ok || string(e2.Value) != "hello" {
		t.Fatalf("copy-out violated: %q", e2.Value)
	}
	// Nil slice in, empty hit out.
	nilKey := mustKey(t, "ns", "nil")
	if err := m.Set(ctx, nilKey, Entry{Value: nil}, Policy{TTL: time.Minute}); err != nil {
		t.Fatal(err)
	}
	e3, ok, err := m.Get(ctx, nilKey)
	if err != nil || !ok || len(e3.Value) != 0 {
		t.Fatalf("nil value must be an empty hit: %q ok=%v err=%v", e3.Value, ok, err)
	}
	// Empty non-nil slice behaves the same.
	emptyKey := mustKey(t, "ns", "empty")
	if err := m.Set(ctx, emptyKey, Entry{Value: []byte{}}, Policy{TTL: time.Minute}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := m.Get(ctx, emptyKey); !ok {
		t.Fatal("empty slice must be a hit")
	}
	// Replacement also copies.
	buf2 := []byte("second")
	if err := m.Set(ctx, key, Entry{Value: buf2}, Policy{TTL: time.Minute}); err != nil {
		t.Fatal(err)
	}
	buf2[0] = 'Z'
	e4, ok, _ := m.Get(ctx, key)
	if !ok || string(e4.Value) != "second" {
		t.Fatalf("replacement copy-in violated: %q", e4.Value)
	}
}

func TestMemoryTTLDeterministic(t *testing.T) {
	cfg := smallCfg()
	clock := newFakeClock()
	m, err := newMemory(cfg, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	ctx := context.Background()
	key := mustKey(t, "ns", "k")
	ttl := 5 * time.Minute

	if err := m.Set(ctx, key, Entry{Value: []byte("v")}, Policy{TTL: ttl}); err != nil {
		t.Fatal(err)
	}
	// Just before expiry: hit.
	clock.advance(ttl - time.Nanosecond)
	if _, ok, err := m.Get(ctx, key); err != nil || !ok {
		t.Fatalf("at TTL-ε want hit, got ok=%v err=%v", ok, err)
	}
	// At exactly the boundary: expired (half-open [stored, stored+TTL)).
	clock.advance(time.Nanosecond)
	if _, ok, err := m.Get(ctx, key); err != nil || ok {
		t.Fatalf("at exactly TTL want miss, got ok=%v err=%v", ok, err)
	}
	// Beyond: still a miss, never stale.
	clock.advance(time.Minute)
	e, ok, err := m.Get(ctx, key)
	if err != nil || ok || e.Value != nil {
		t.Fatalf("beyond TTL want miss, got entry=%v ok=%v err=%v", e, ok, err)
	}
}

func TestMemoryTTLNotSliding(t *testing.T) {
	cfg := smallCfg()
	clock := newFakeClock()
	m, err := newMemory(cfg, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	ctx := context.Background()
	key := mustKey(t, "ns", "k")
	ttl := 10 * time.Minute
	if err := m.Set(ctx, key, Entry{Value: []byte("v")}, Policy{TTL: ttl}); err != nil {
		t.Fatal(err)
	}
	// Repeated hits close to expiry do not extend it.
	for i := 0; i < 5; i++ {
		clock.advance(ttl / 2)
		if _, ok, err := m.Get(ctx, key); err != nil || !ok {
			t.Fatalf("hit %d at half-TTL should hit: ok=%v err=%v", i, ok, err)
		}
		clock.advance(-ttl / 2)
	}
	// A miss does not refresh anything either.
	clock.advance(ttl)
	if _, ok, _ := m.Get(ctx, key); ok {
		t.Fatal("entry should be expired after fixed TTL despite repeated reads")
	}
}

func TestMemoryReplacementResetsDeadline(t *testing.T) {
	cfg := smallCfg()
	clock := newFakeClock()
	m, err := newMemory(cfg, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	ctx := context.Background()
	key := mustKey(t, "ns", "k")
	if err := m.Set(ctx, key, Entry{Value: []byte("a")}, Policy{TTL: time.Minute}); err != nil {
		t.Fatal(err)
	}
	clock.advance(30 * time.Second)
	if err := m.Set(ctx, key, Entry{Value: []byte("b")}, Policy{TTL: time.Hour}); err != nil {
		t.Fatal(err)
	}
	clock.advance(30 * time.Minute) // past the first TTL, within the second
	e, ok, _ := m.Get(ctx, key)
	if !ok || string(e.Value) != "b" {
		t.Fatalf("replacement should have reset deadline: ok=%v value=%q", ok, e.Value)
	}
	// Updating an EXPIRED key behaves like inserting fresh.
	clock.advance(time.Hour)
	if err := m.Set(ctx, key, Entry{Value: []byte("c")}, Policy{TTL: time.Minute}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := m.Get(ctx, key); !ok {
		t.Fatal("set over expired key must work like an insert")
	}
}

func TestMemoryLRUByCount(t *testing.T) {
	cfg := MemoryConfig{MaxEntries: 2, MaxBytes: 1 << 20}
	m, err := NewMemory(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	ctx := context.Background()
	a, b, c := mustKey(t, "ns", "a"), mustKey(t, "ns", "b"), mustKey(t, "ns", "c")

	// Sequence 1: Get refreshes recency.
	set := func(k Key) { _ = m.Set(ctx, k, Entry{Value: []byte("v")}, Policy{TTL: time.Minute}) }
	set(a)
	set(b)
	if _, ok, _ := m.Get(ctx, a); !ok {
		t.Fatal("A missing before eviction test")
	}
	set(c) // evicts B (LRU), not A
	if _, ok, _ := m.Get(ctx, a); !ok {
		t.Fatal("A (recently read) was evicted instead of B")
	}
	if _, ok, _ := m.Get(ctx, b); ok {
		t.Fatal("B (least recently used) survived eviction")
	}
	if _, ok, _ := m.Get(ctx, c); !ok {
		t.Fatal("C missing after insertion")
	}

	// Sequence 2: Set/update refreshes recency too.
	d, e := mustKey(t, "ns", "d"), mustKey(t, "ns", "e")
	set(d) // evicts A or C... both were read; LRU among them
	set(e)
	if _, ok, _ := m.Get(ctx, d); !ok {
		t.Fatal("D missing")
	}
	set(c) // updates C's recency via Set; evicts whatever is LRU
	if _, ok, _ := m.Get(ctx, c); !ok {
		t.Fatal("C missing after update")
	}
}

func TestMemoryLRUUpdateDoesNotDuplicate(t *testing.T) {
	cfg := MemoryConfig{MaxEntries: 3, MaxBytes: 1 << 20}
	m, err := NewMemory(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	ctx := context.Background()
	a, b := mustKey(t, "ns", "a"), mustKey(t, "ns", "b")
	_ = m.Set(ctx, a, Entry{Value: []byte("v")}, Policy{TTL: time.Minute})
	_ = m.Set(ctx, b, Entry{Value: []byte("v")}, Policy{TTL: time.Minute})
	// Update A repeatedly: entry count must stay 2, not grow.
	for i := 0; i < 10; i++ {
		_ = m.Set(ctx, a, Entry{Value: []byte(fmt.Sprintf("v%d", i))}, Policy{TTL: time.Minute})
	}
	if len(m.entries) != 2 {
		t.Fatalf("updates created duplicate entries: count=%d", len(m.entries))
	}
	e, ok, _ := m.Get(ctx, a)
	if !ok || string(e.Value) != "v9" {
		t.Fatalf("last write should win: %q", e.Value)
	}
}

func TestMemoryEvictionByBytes(t *testing.T) {
	// Room for roughly two entries of these sizes.
	entry := int64(entryOverhead + len("ns:payload-key") + 64)
	cfg := MemoryConfig{
		MaxEntries:    100,
		MaxBytes:      2*entry + entryOverhead, // enough slack for keys
		MaxEntryBytes: entry,
	}
	m, err := NewMemory(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	ctx := context.Background()
	val := make([]byte, 64)
	keys := make([]Key, 3)
	for i := range keys {
		k, _ := NewKey("ns", strconv.Itoa(i))
		keys[i] = k
	}
	if err := m.Set(ctx, keys[0], Entry{Value: val}, Policy{TTL: time.Minute}); err != nil {
		t.Fatalf("first set: %v", err)
	}
	if err := m.Set(ctx, keys[1], Entry{Value: val}, Policy{TTL: time.Minute}); err != nil {
		t.Fatalf("second set: %v (usage may exceed; check accounting)", err)
	}
	// Third insert evicts keys[0], keeping usage within bound.
	if err := m.Set(ctx, keys[2], Entry{Value: val}, Policy{TTL: time.Minute}); err != nil {
		t.Fatalf("third set: %v", err)
	}
	if _, ok, _ := m.Get(ctx, keys[0]); ok {
		t.Fatal("oldest entry survived byte-capacity eviction")
	}
	if _, ok, _ := m.Get(ctx, keys[2]); !ok {
		t.Fatal("newest entry missing")
	}
	if m.usage > cfg.MaxBytes {
		t.Fatalf("usage %d exceeds bound %d after Set", m.usage, cfg.MaxBytes)
	}
}

func TestMemoryEntrySizeBoundaries(t *testing.T) {
	perEntry := int64(4096)
	cfg := MemoryConfig{MaxEntries: 100, MaxBytes: perEntry * 8, MaxEntryBytes: perEntry}
	m, err := NewMemory(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	ctx := context.Background()

	sizedKey := func(n int) Key {
		// Component of n 'a' bytes (valid key text).
		k, err := NewKey("ns", strings.Repeat("a", n))
		if err != nil {
			t.Fatal(err)
		}
		return k
	}

	// Exactly at the per-entry limit: key length + value + overhead == limit.
	k := sizedKey(100)
	valueLen := int(perEntry) - entryOverhead - len(k)
	if err := m.Set(ctx, k, Entry{Value: make([]byte, valueLen)}, Policy{TTL: time.Minute}); err != nil {
		t.Fatalf("entry at exactly the per-entry limit rejected: %v", err)
	}
	// One byte above.
	if err := m.Set(ctx, k, Entry{Value: make([]byte, valueLen+1)}, Policy{TTL: time.Minute}); !errors.Is(err, ErrEntryTooLarge) {
		t.Fatalf("one byte over per-entry limit: got %v", err)
	}
	// The previous (at-limit) entry survives the rejected replacement.
	if e, ok, _ := m.Get(ctx, k); !ok || len(e.Value) != valueLen {
		t.Fatalf("rejected replacement corrupted previous entry: ok=%v len=%d", ok, len(e.Value))
	}

	// Cannot fit even in an empty store: bigger than MaxBytes.
	hugeCfg := MemoryConfig{MaxEntries: 100, MaxBytes: 1 << 20, MaxEntryBytes: 0}
	hm, err := NewMemory(hugeCfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { hm.Close() })
	hk := mustKey(t, "ns", "huge")
	if err := hm.Set(ctx, hk, Entry{Value: make([]byte, 2<<20)}, Policy{TTL: time.Minute}); !errors.Is(err, ErrEntryTooLarge) {
		t.Fatalf("entry larger than whole cache: got %v", err)
	}
	if _, ok, _ := hm.Get(ctx, hk); ok {
		t.Fatal("rejected oversized entry stored anyway")
	}
	// Store still healthy after the rejection.
	if err := hm.Set(ctx, hk, Entry{Value: []byte("small")}, Policy{TTL: time.Minute}); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryExpiredReclaimedBeforeLiveEviction(t *testing.T) {
	cfg := MemoryConfig{MaxEntries: 2, MaxBytes: 1 << 20}
	clock := newFakeClock()
	m, err := newMemory(cfg, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	ctx := context.Background()
	a, b, c := mustKey(t, "ns", "a"), mustKey(t, "ns", "b"), mustKey(t, "ns", "c")

	if err := m.Set(ctx, a, Entry{Value: []byte("va")}, Policy{TTL: time.Minute}); err != nil {
		t.Fatal(err)
	}
	if err := m.Set(ctx, b, Entry{Value: []byte("vb")}, Policy{TTL: time.Hour}); err != nil {
		t.Fatal(err)
	}
	// A expires; B stays live. Advance past A's TTL.
	clock.advance(2 * time.Minute)
	// Insert C under capacity pressure: A (expired) must go before B (live).
	if err := m.Set(ctx, c, Entry{Value: []byte("vc")}, Policy{TTL: time.Minute}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := m.Get(ctx, b); !ok {
		t.Fatal("live entry B evicted while expired A held capacity")
	}
	if _, ok, _ := m.Get(ctx, c); !ok {
		t.Fatal("C missing after insertion")
	}
	// Accounting for A was reclaimed.
	if len(m.entries) != 2 {
		t.Fatalf("expired entry A still allocated: entries=%d", len(m.entries))
	}
}

func TestMemoryErrorPrecedence(t *testing.T) {
	m, err := NewMemory(smallCfg())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	ctx := context.Background()
	key := mustKey(t, "ns", "k")

	// Nil context beats everything, including closed.
	closed, err := NewMemory(smallCfg())
	if err != nil {
		t.Fatal(err)
	}
	_ = closed.Close()
	if _, _, err := closed.Get(nil, key); !errors.Is(err, ErrNilContext) {
		t.Fatalf("nil ctx on closed store: %v", err)
	}

	// Canceled context beats invalid key/policy and closure.
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := closed.Get(canceled, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled ctx should beat invalid key: %v", err)
	}
	if err := closed.Set(canceled, "", Entry{}, Policy{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Set: %v", err)
	}

	// Invalid key beats invalid policy.
	if err := m.Set(ctx, "", Entry{}, Policy{}); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("key should precede policy: %v", err)
	}
	// Invalid policy beats closure.
	if err := closed.Set(ctx, key, Entry{}, Policy{}); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("policy should precede closed: %v", err)
	}
	// Closed on otherwise-valid input.
	if _, _, err := closed.Get(ctx, key); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed Get: %v", err)
	}
	if err := closed.Set(ctx, key, Entry{Value: []byte("v")}, Policy{TTL: time.Minute}); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed Set: %v", err)
	}
	if err := closed.Delete(ctx, key); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed Delete: %v", err)
	}
	// Deadline exceeded propagates as itself.
	expired, cancel2 := context.WithTimeout(context.Background(), 0)
	defer cancel2()
	if _, _, err := m.Get(expired, key); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired ctx: %v", err)
	}
}

func TestMemoryDeleteSemantics(t *testing.T) {
	m, err := NewMemory(smallCfg())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	ctx := context.Background()
	key := mustKey(t, "ns", "k")
	if err := m.Delete(ctx, key); err != nil {
		t.Fatalf("delete never-set key: %v", err)
	}
	if err := m.Set(ctx, key, Entry{Value: []byte("v")}, Policy{TTL: time.Minute}); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := m.Get(ctx, key); ok {
		t.Fatal("deleted key readable")
	}
	if err := m.Delete(ctx, key); err != nil {
		t.Fatalf("repeated delete not idempotent: %v", err)
	}
	if m.usage != 0 {
		t.Fatalf("usage not released: %d", m.usage)
	}
}

func TestMemoryDeleteAfterExpiration(t *testing.T) {
	cfg := smallCfg()
	clock := newFakeClock()
	m, err := newMemory(cfg, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	ctx := context.Background()
	key := mustKey(t, "ns", "k")
	if err := m.Set(ctx, key, Entry{Value: []byte("v")}, Policy{TTL: time.Minute}); err != nil {
		t.Fatal(err)
	}
	clock.advance(time.Hour)
	if err := m.Delete(ctx, key); err != nil {
		t.Fatalf("delete expired key: %v", err)
	}
	if m.usage != 0 {
		t.Fatalf("expired delete left usage: %d", m.usage)
	}
}

func TestMemoryCloseLifecycle(t *testing.T) {
	m, err := NewMemory(smallCfg())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	key := mustKey(t, "ns", "k")
	if err := m.Set(ctx, key, Entry{Value: []byte("v")}, Policy{TTL: time.Minute}); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	// Idempotent.
	if err := m.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	// Operations rejected with ErrClosed.
	if _, _, err := m.Get(ctx, key); !errors.Is(err, ErrClosed) {
		t.Fatalf("post-close Get: %v", err)
	}
	if err := m.Delete(ctx, key); !errors.Is(err, ErrClosed) {
		t.Fatalf("post-close Delete: %v", err)
	}
}

func TestMemoryInstancesIndependent(t *testing.T) {
	a, err := NewMemory(smallCfg())
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewMemory(smallCfg())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close(); b.Close() })
	ctx := context.Background()
	key := mustKey(t, "ns", "k")
	if err := a.Set(ctx, key, Entry{Value: []byte("A")}, Policy{TTL: time.Minute}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := b.Get(ctx, key); ok {
		t.Fatal("stores share state")
	}
	// Closing one does not affect the other.
	_ = a.Close()
	if _, ok, _ := b.Get(ctx, key); ok {
		t.Fatal("b affected by a's close")
	}
}

// TestMemoryConcurrentOperations is the race-detector workload: concurrent
// readers, writers, replacers, deleters, expirers, and a closer, all against
// overlapping key sets. Correctness assertions are post-conditions only —
// every store invariant observable from outside must hold.
func TestMemoryConcurrentOperations(t *testing.T) {
	m, err := NewMemory(MemoryConfig{MaxEntries: 128, MaxBytes: 1 << 16, MaxEntryBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const workers = 16
	const iterations = 200

	var wg sync.WaitGroup
	run := func(f func(i int)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				f(i)
			}
		}()
	}
	for w := 0; w < workers; w++ {
		switch w % 5 {
		case 0: // writers on shared keys
			run(func(i int) {
				k := mustKey(t, "ns", strconv.Itoa(i%8))
				_ = m.Set(ctx, k, Entry{Value: []byte("value")}, Policy{TTL: time.Minute})
			})
		case 1: // readers on shared keys
			run(func(i int) {
				k := mustKey(t, "ns", strconv.Itoa(i%8))
				e, ok, err := m.Get(ctx, k)
				if err != nil && !errors.Is(err, ErrClosed) {
					t.Errorf("Get during churn: %v", err)
				}
				if ok && e.Value == nil {
					t.Errorf("hit returned nil slice")
				}
				// Writers race between "value" and variable-size replacements,
				// and the closer may close mid-run; a whole value or a clean
				// miss/close are all consistent observations.
			})
		case 2: // deleters
			run(func(i int) {
				_ = m.Delete(ctx, mustKey(t, "ns", strconv.Itoa(i%8)))
			})
		case 3: // replacers with varying sizes (replacement growth/shrinkage)
			run(func(i int) {
				k := mustKey(t, "ns", strconv.Itoa(i%8))
				size := (i % 7) * 64
				_ = m.Set(ctx, k, Entry{Value: make([]byte, size)}, Policy{TTL: time.Minute})
			})
		case 4: // short-TTL writers to force expiry during access
			run(func(i int) {
				k := mustKey(t, "ns", "short")
				_ = m.Set(ctx, k, Entry{Value: []byte("s")}, Policy{TTL: time.Nanosecond})
			})
		}
	}
	// Closer running concurrently near the end (after most operations).
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(20 * time.Millisecond) // bounded overlap, not correctness-critical
		_ = m.Close()
	}()
	wg.Wait()
	// After everything settles, the store must be consistent.
	if _, ok, err := m.Get(ctx, mustKey(t, "ns", "0")); err != nil && !errors.Is(err, ErrClosed) {
		t.Fatalf("post-race Get: %v", err)
	} else if ok && err != nil {
		t.Fatalf("inconsistent result: ok=%v err=%v", ok, err)
	}
}

func TestMemoryUsageStaysBounded(t *testing.T) {
	cfg := MemoryConfig{MaxEntries: 32, MaxBytes: 16 * 1024, MaxEntryBytes: 4096}
	m, err := NewMemory(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	ctx := context.Background()
	for i := 0; i < 1000; i++ {
		k, _ := NewKey("ns", strconv.Itoa(i%64)) // forces churn beyond capacity
		if err := m.Set(ctx, k, Entry{Value: make([]byte, 128)}, Policy{TTL: time.Minute}); err != nil {
			// ErrEntryTooLarge impossible here (128+key < 4096): report others.
			if errors.Is(err, ErrEntryTooLarge) {
				t.Fatalf("unexpected size rejection: %v", err)
			}
		}
	}
	m.mu.Lock()
	usage, count := m.usage, len(m.entries)
	m.mu.Unlock()
	if usage > cfg.MaxBytes {
		t.Fatalf("usage %d exceeds MaxBytes %d after churn", usage, cfg.MaxBytes)
	}
	if count > cfg.MaxEntries {
		t.Fatalf("count %d exceeds MaxEntries %d after churn", count, cfg.MaxEntries)
	}
}

// --- Benchmarks ---

func benchMemory(b *testing.B) *Memory {
	m, err := NewMemory(MemoryConfig{MaxEntries: 4096, MaxBytes: 1 << 22, MaxEntryBytes: 8192})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { m.Close() })
	return m
}

func BenchmarkMemoryGetHit(b *testing.B) {
	m := benchMemory(b)
	ctx := context.Background()
	key := mustKeyB(b, "ns", "hit")
	_ = m.Set(ctx, key, Entry{Value: make([]byte, 256)}, Policy{TTL: time.Hour})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = m.Get(ctx, key)
	}
}

func BenchmarkMemoryGetMiss(b *testing.B) {
	m := benchMemory(b)
	ctx := context.Background()
	miss := mustKeyB(b, "ns", "miss")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = m.Get(ctx, miss)
	}
}

func BenchmarkMemorySet(b *testing.B) {
	m := benchMemory(b)
	ctx := context.Background()
	key := mustKeyB(b, "ns", "set")
	val := make([]byte, 256)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.Set(ctx, key, Entry{Value: val}, Policy{TTL: time.Hour})
	}
}

func BenchmarkMemorySetChurn(b *testing.B) {
	// Sets that keep changing keys force LRU work every time.
	m := benchMemory(b)
	ctx := context.Background()
	keys := make([]Key, 8192)
	for i := range keys {
		k, _ := NewKey("ns", strconv.Itoa(i))
		keys[i] = k
	}
	val := make([]byte, 256)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.Set(ctx, keys[i%len(keys)], Entry{Value: val}, Policy{TTL: time.Hour})
	}
}

func BenchmarkMemoryMixed(b *testing.B) {
	m := benchMemory(b)
	ctx := context.Background()
	keys := make([]Key, 1024)
	for i := range keys {
		k, _ := NewKey("ns", strconv.Itoa(i))
		keys[i] = k
	}
	val := make([]byte, 256)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		k := keys[i%len(keys)]
		if i%4 == 3 {
			_ = m.Set(ctx, k, Entry{Value: val}, Policy{TTL: time.Hour})
		} else {
			_, _, _ = m.Get(ctx, k)
		}
	}
}

func BenchmarkMemoryDelete(b *testing.B) {
	m := benchMemory(b)
	ctx := context.Background()
	key := mustKeyB(b, "ns", "del")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.Delete(ctx, key)
	}
}

func mustKeyB(b *testing.B, ns string, components ...string) Key {
	k, err := NewKey(ns, components...)
	if err != nil {
		b.Fatal(err)
	}
	return k
}

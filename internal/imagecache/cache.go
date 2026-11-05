// Package imagecache implements the F05 optimized-image disk cache: a
// process-local, bounded, root-confined store for completed encoded image
// outputs. Repeated requests for the same source content and transformation
// reuse previously encoded bytes without rerunning decode, resize, or native
// encoding.
//
// Trust model and boundaries:
//
//   - The cache directory is writable SERVER data owned by one process.
//     Multi-process sharing of one directory is explicitly unsupported
//     (§36 of the task spec); coordination would need cross-process locks.
//   - The directory is trusted: an attacker who can modify it can already
//     replace entries wholesale. Integrity checking detects ACCIDENTAL
//     corruption (empty files, missing magic, format mixups), not a
//     deliberate attacker. Hit verification checks format magic, exact
//     size bound, and regular-file status; publication validates before
//     making an entry visible. No cryptographic integrity is claimed.
//   - Identity keys come from imageopt.CacheIdentity.Key (SHA-256 over
//     canonical fields). Filenames are derived exclusively from those
//     digests — raw query text, source paths, or user-controlled strings
//     never become filenames.
//
// Layout: <root>/<shard>/<key>.<format> where shard is the first two hex
// digits of the key body (256 shard directories, keeping directory
// listings small). The format extension means one digest can hold distinct
// WebP and AVIF entries simultaneously. Temporary files live in the
// destination shard with a reserved "tmp-" prefix; startup reconciliation
// removes leftovers from crashed writers.
//
// Durability: published entries are fsynced before rename, but the
// directory entry itself is not fsynced — a crash immediately after
// publication may lose the newest entry, which is rebuilt on the next
// miss. Acceptable for a rebuildable cache; documented deliberately.
package imagecache

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/akashKarmakar02/meta/imageopt"
)

// Errors returned to callers. The HTTP layer maps every one of them onto
// the miss/fallback path; none of them ever reach a client verbatim.
var (
	// ErrCacheMiss: no usable entry for the key and format (absent,
	// corrupt, oversized, or concurrently evicted). Corrupt entries are
	// removed before this is returned so the next identical request
	// republishes fresh bytes.
	ErrCacheMiss = errors.New("imagecache: cache miss")
	// ErrCacheClosed: Get or Put after Close.
	ErrCacheClosed = errors.New("imagecache: cache closed")
)

// Config describes the disk cache. Every limit must be positive — there is
// no accidental unlimited storage. An empty Dir is rejected: caching is
// opt-in and the operator chooses the directory.
type Config struct {
	// Dir is the dedicated cache directory. The application must keep it
	// separate from public source roots; Open cannot know those roots.
	Dir string
	// MaxTotalBytes bounds the sum of finalized entry sizes. Metadata,
	// directories, and transient temporary files consume additional space.
	MaxTotalBytes int64
	// MaxEntries bounds the number of live entries.
	MaxEntries int
	// MaxEntryBytes bounds one entry. Should match the endpoint's output cap.
	MaxEntryBytes int64
}

// Validate enforces positive limits and a non-empty directory.
func (c Config) Validate() error {
	if strings.TrimSpace(c.Dir) == "" {
		return fmt.Errorf("imagecache: cache directory must be configured")
	}
	if c.MaxTotalBytes < 1 {
		return fmt.Errorf("imagecache: MaxTotalBytes must be positive")
	}
	if c.MaxEntries < 1 {
		return fmt.Errorf("imagecache: MaxEntries must be positive")
	}
	if c.MaxEntryBytes < 1 {
		return fmt.Errorf("imagecache: MaxEntryBytes must be positive")
	}
	if c.MaxEntryBytes > c.MaxTotalBytes {
		return fmt.Errorf("imagecache: MaxEntryBytes may not exceed MaxTotalBytes")
	}
	return nil
}

// shardCount is fixed by the layout; shards are created on demand at
// publication.
const shardCount = 256

const (
	filePerm   os.FileMode = 0o600  // cache entries: owner read/write only
	dirPerm    os.FileMode = 0o700  // cache directories: owner only
	tempPrefix             = "tmp-" // reserved prefix for unpublished files
)

// entry is one live, accounted cache entry, indexed by its root-relative
// path. The path embeds both the identity digest and the concrete output
// format, so distinct formats never collide in accounting or lookup.
type entry struct {
	key  string // identity digest, for diagnostics
	size int64
}

// Cache is a process-local optimized-image disk cache. Safe for concurrent
// use; one instance owns its directory. Get/Put serialize only bookkeeping
// and metadata operations, never image encoding or whole-file reads (§34):
// encoded output is produced before Put is called, and hit reads stream from
// an already-open descriptor outside the accounting lock, so one slow
// reader cannot block unrelated hits.
type Cache struct {
	root *os.Root // confines every read, write, rename, and remove

	mu      sync.Mutex
	closed  bool
	entries map[string]*entry // rel path -> entry (validated live entries only)
	total   int64             // sum of entry sizes
	cfg     Config
}

// Open validates the configuration, creates (or reopens) the cache
// directory, and reconciles existing state (§31):
//
//   - A symlink at the cache root is rejected outright: the operator named
//     a directory, and following a link would relocate the writable root.
//   - The directory is created with 0700 when missing (parent must be
//     writable by the server user).
//   - Writability is proven with a real create/remove cycle, so a
//     read-only filesystem fails here at startup, not on first publish.
//   - Entries matching the expected name pattern are re-indexed at their
//     real sizes; anything else (foreign filenames, directories, symlinks)
//     is ignored — never read, never served, never deleted (the operator
//     cleans their own directory).
//   - Abandoned tmp-* temporary files from crashed writers are removed.
//   - Capacity is enforced by evicting least-recently-used entries.
//
// Recency is the file modification time: hits bump it (bounded, one
// metadata write per hit), so recency survives restarts. Path order breaks
// ties deterministically.
func Open(cfg Config) (*Cache, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	root := filepath.Clean(cfg.Dir)

	// Reject a symlinked cache root before creating or opening anything.
	if st, err := os.Lstat(root); err == nil {
		if st.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("imagecache: cache root %s is a symlink", root)
		}
		if !st.IsDir() {
			return nil, fmt.Errorf("imagecache: cache root %s is not a directory", root)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("imagecache: cache root %s: %w", root, err)
	}
	if err := os.MkdirAll(root, dirPerm); err != nil {
		return nil, fmt.Errorf("imagecache: create cache dir %s: %w", root, err)
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("imagecache: open cache root %s: %w", root, err)
	}
	c := &Cache{root: r, entries: map[string]*entry{}, cfg: cfg}
	if err := c.reconcile(); err != nil {
		_ = r.Close()
		return nil, err
	}
	return c, nil
}

// Close releases the root handle. In-flight Get readers keep their open
// descriptors; publications that already completed remain durable.
func (c *Cache) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	return c.root.Close()
}

// Get returns the cached bytes for key in the expected concrete format,
// verified against the format's magic bytes and within the per-entry size
// bound. Corruption (wrong magic, empty, oversize, non-regular file)
// is treated as a miss after a best-effort removal of the bad entry, so
// the next identical request republishes instead of serving bad bytes
// forever (§26). Removal failure never serves the corrupt entry — it
// stays a miss.
//
// The read streams from a descriptor opened under the accounting lock and
// read outside it: eviction between open and read cannot truncate the
// bytes this caller observes (POSIX unlink semantics keep the descriptor
// valid).
func (c *Cache) Get(ctx context.Context, key string, expect imageopt.Format, maxEntryBytes int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rel, err := relativeName(key, expect)
	if err != nil {
		return nil, ErrCacheMiss
	}
	if maxEntryBytes < 1 {
		maxEntryBytes = c.cfg.MaxEntryBytes
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrCacheClosed
	}
	if _, ok := c.entries[rel]; !ok {
		c.mu.Unlock()
		return nil, ErrCacheMiss
	}
	entryInfo, err := c.root.Lstat(rel)
	if err != nil || !entryInfo.Mode().IsRegular() {
		c.dropEntryLocked(rel)
		if err == nil {
			_ = c.root.Remove(rel)
		}
		c.mu.Unlock()
		return nil, ErrCacheMiss
	}
	f, err := c.root.Open(rel) // confined open; descriptor outlives eviction
	if err != nil {
		c.mu.Unlock()
		return nil, ErrCacheMiss // evicted between lookup and open
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || !os.SameFile(entryInfo, st) {
		_ = f.Close()
		c.mu.Unlock()
		return nil, ErrCacheMiss
	}
	c.mu.Unlock()
	defer f.Close()
	// Bounded read: at most the cap plus one byte so an exactly-at-limit
	// entry succeeds and anything larger is detected.
	buf, err := readAllBounded(f, maxEntryBytes+1)
	if err != nil {
		c.removeBadEntry(rel, st)
		return nil, ErrCacheMiss
	}
	if int64(len(buf)) > maxEntryBytes || len(buf) == 0 || !validFormatMagic(expect, buf) {
		c.removeBadEntry(rel, st)
		return nil, ErrCacheMiss
	}

	// Recency bump: one bounded metadata write per hit. Failures do not
	// fail the hit — worst case the entry is evicted sooner than ideal.
	c.bumpRecency(rel)
	out := make([]byte, len(buf))
	copy(out, buf)
	return out, nil
}

// Put atomically publishes data under key with the concrete format. The
// data must already be fully encoded and validated by the caller; Put
// re-verifies non-empty, size-bounded, and format-magic-consistent bytes
// before making anything visible (§21).
//
// The publication sequence: create a unique temporary file in the
// destination shard (same filesystem as the final name), write all bytes,
// fsync, then root-relative rename to the final name. Readers observe
// either the previous complete entry or the new complete entry, never a
// partial file. A failed write removes its temporary file and leaves any
// existing entry untouched.
func (c *Cache) Put(ctx context.Context, key string, format imageopt.Format, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	rel, err := relativeName(key, format)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return fmt.Errorf("imagecache: refusing to publish empty entry")
	}
	if int64(len(data)) > c.cfg.MaxEntryBytes {
		return fmt.Errorf("imagecache: entry exceeds MaxEntryBytes")
	}
	if !validFormatMagic(format, data) {
		return fmt.Errorf("imagecache: data does not match the claimed format")
	}
	shard := shardOf(key)

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrCacheClosed
	}
	needShard := true
	if _, err := c.root.Stat(shard); err == nil {
		needShard = false
	}
	c.mu.Unlock()
	if needShard {
		if err := c.root.MkdirAll(shard, dirPerm); err != nil {
			return fmt.Errorf("imagecache: create shard: %w", err)
		}
	}

	tmp, err := writeTemp(c.root, shard, format, data)
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		_ = c.root.Remove(tmp)
		return ErrCacheClosed
	}
	if err := ctx.Err(); err != nil {
		_ = c.root.Remove(tmp)
		return err
	}
	// Same-filesystem atomic publication via root-relative rename.
	if err := c.root.Rename(tmp, rel); err != nil {
		_ = c.root.Remove(tmp)
		return fmt.Errorf("imagecache: publish: %w", err)
	}
	c.indexLocked(key, rel, int64(len(data)))
	c.evictLocked()
	return nil
}

// indexLocked records a published entry, correcting accounting if a file
// was replaced at the same path (rename over an existing entry).
func (c *Cache) indexLocked(key, rel string, size int64) {
	if old, ok := c.entries[rel]; ok {
		c.total -= old.size
		delete(c.entries, rel)
	}
	c.entries[rel] = &entry{key: key, size: size}
	c.total += size
	if c.total < 0 { // defensive: accounting must never go negative
		c.total = 0
	}
}

// Stats reports live accounting (finalized entries only; the caller must
// not rely on it covering temporary or foreign files).
func (c *Cache) Stats() (entries int, totalBytes int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries), c.total
}

// removeBadEntry deletes a corrupt/unusable entry and drops it from
// accounting. Failures still leave the entry unserved: it is gone from the
// index, so corrupt bytes are never returned twice.
func (c *Cache) removeBadEntry(rel string, observed os.FileInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()
	current, err := c.root.Lstat(rel)
	if err != nil || !os.SameFile(current, observed) {
		return // a replacement must not be removed by an older reader
	}
	c.dropEntryLocked(rel)
	_ = c.root.Remove(rel)
}

func (c *Cache) dropEntryLocked(rel string) {
	if e, ok := c.entries[rel]; ok {
		delete(c.entries, rel)
		c.total -= e.size
		if c.total < 0 {
			c.total = 0
		}
	}
}

// bumpRecency marks an entry used. Runs after the bytes were successfully
// returned, so the metadata write cannot turn a good hit into a failure.
func (c *Cache) bumpRecency(rel string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.entries[rel]; !ok {
		return
	}
	now := time.Now()
	_ = c.root.Chtimes(rel, now, now)
}

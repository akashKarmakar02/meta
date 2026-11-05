package imagecache

import (
	"sort"
)

// Eviction (§30): least-recently-used by the file's modification time,
// which hit-path recency bumps keep fresh, so recency survives restarts.
// Ties break on path for determinism. Eviction runs inside the accounting
// lock at publication time and during startup reconciliation — there is no
// background sweeper, no unbounded scan on hits, and no eviction lock that
// blocks requests.
//
// The just-published entry is never the one evicted first: it carries the
// newest mtime in the shard set, so the LRU order reaches it last.
//
// Readers of a concurrently evicted entry are safe by POSIX semantics:
// Get opens the descriptor before releasing the accounting lock, and
// unlink does not invalidate open descriptors — the reader observes the
// complete bytes of the evicted entry.

// evictLocked enforces MaxTotalBytes and MaxEntries. Caller holds c.mu.
func (c *Cache) evictLocked() {
	if len(c.entries) == 0 {
		return
	}
	if c.total <= c.cfg.MaxTotalBytes && len(c.entries) <= c.cfg.MaxEntries {
		return
	}

	type cand struct {
		key  string
		rel  string
		mt   int64 // modification time, nanoseconds
		size int64
	}
	cands := make([]cand, 0, len(c.entries))
	for rel, e := range c.entries {
		var mt int64
		if st, err := c.root.Stat(rel); err == nil {
			mt = st.ModTime().UnixNano()
		} else {
			// Unstatable entries are the best eviction candidates.
			mt = -1 << 62
		}
		cands = append(cands, cand{key: e.key, rel: rel, mt: mt, size: e.size})
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].mt != cands[j].mt {
			return cands[i].mt < cands[j].mt // oldest first
		}
		return cands[i].rel < cands[j].rel
	})

	for _, x := range cands {
		if c.total <= c.cfg.MaxTotalBytes && len(c.entries) <= c.cfg.MaxEntries {
			return
		}
		if err := c.root.Remove(x.rel); err != nil {
			continue // unremovable now; not served again unless it re-validates
		}
		delete(c.entries, x.rel)
		c.total -= x.size
		if c.total < 0 {
			c.total = 0
		}
	}
}

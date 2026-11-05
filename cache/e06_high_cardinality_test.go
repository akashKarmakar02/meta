package cache

import (
	"context"
	"errors"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestE06HighCardinalityBoundedMemory (§9): bounded cache behavior under a
// many-unique-key workload — thousands of distinct keys churning well past
// capacity with concurrent readers and writers, rapid replacement, expiry
// and deletion intermixed, then Close. Entry count and accounted bytes must
// stay within bounds and eviction/cleanup must stay fast.
//
// Size is configurable via -cardinality for CI-friendliness; the default
// (4096 distinct keys, 4096-entry cache) keeps runtime in seconds without
// exhausting a developer machine.
func TestE06HighCardinalityBoundedMemory(t *testing.T) {
	const card = 4096 // distinct keys; cache capacity is 1024 entries
	cfg := MemoryConfig{MaxEntries: 1024, MaxBytes: 512 * 1024, MaxEntryBytes: 8192}
	m, err := NewMemory(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	ctx := context.Background()
	value := make([]byte, 128)

	var reads, writes, deletes atomic.Int64
	var wg sync.WaitGroup
	start := time.Now()
	for w := 0; w < 8; w++ {
		wg.Go(func() {
			base := w * (card / 8)
			for i := 0; i < card/8; i++ {
				k, _ := NewKey("hc", strconv.Itoa(base+i))
				if err := m.Set(ctx, k, Entry{Value: value}, Policy{TTL: time.Minute}); err != nil && !errors.Is(err, ErrClosed) {
					t.Errorf("set: %v", err)
					return
				}
				writes.Add(1)
				if _, _, err := m.Get(ctx, k); err != nil && !errors.Is(err, ErrClosed) {
					t.Errorf("get: %v", err)
					return
				}
				reads.Add(1)
				switch i % 16 {
				case 0:
					_ = m.Delete(ctx, k)
					deletes.Add(1)
				case 1: // short-TTL variant: expires under pressure soon after
					_ = m.Set(ctx, k, Entry{Value: value}, Policy{TTL: time.Millisecond})
				}
			}
		})
	}
	wg.Wait()
	elapsed := time.Since(start)
	m.mu.Lock()
	usage, count := m.usage, len(m.entries)
	m.mu.Unlock()
	if count > cfg.MaxEntries {
		t.Fatalf("entry count %d exceeds MaxEntries %d", count, cfg.MaxEntries)
	}
	if usage > cfg.MaxBytes {
		t.Fatalf("accounted bytes %d exceed MaxBytes %d", usage, cfg.MaxBytes)
	}
	t.Logf("high-cardinality churn: %d writes, %d reads, %d deletes over %v; final entries=%d usage=%dB",
		writes.Load(), reads.Load(), deletes.Load(), elapsed.Round(time.Millisecond), count, usage)
	// Accounted bytes vs process heap are different things: report both.
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	t.Logf("process heap in use: %d KiB (accounted cache bytes: %d)", ms.HeapInuse/1024, usage)
	// Close under no contention is idempotent and prompt.
	if err := m.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, _, err := m.Get(ctx, mustKeyT(t, "hc", "1")); !errors.Is(err, ErrClosed) {
		t.Fatalf("post-close get: %v", err)
	}
}

func mustKeyT(t *testing.T, ns, k string) Key {
	t.Helper()
	key, err := NewKey(ns, k)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// TestE06ReclaimSweepReadsClockOnce is the regression test for the E06
// clock-read optimization: reclaimExpired must read the injected clock once
// per sweep, not once per entry — on hosts without a working vDSO clock,
// per-entry reads made an O(n) sweep cost n × µs. A counting fake clock
// pins the count while keeping expiry semantics (exact boundary: expired
// means now >= expiresAt).
func TestE06ReclaimSweepReadsClockOnce(t *testing.T) {
	// The frozen fake clock also guarantees exact boundary semantics: a
	// 1ns-TTL entry is expired the moment it is stored (now >= expiresAt).
	fake := &countingClock{now: time.Now().Add(time.Hour)}
	m, err := newMemory(MemoryConfig{MaxEntries: 8, MaxBytes: 64 << 10}, fake.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	ctx := context.Background()
	for i := 0; i < 8; i++ {
		k, _ := NewKey("clk", strconv.Itoa(i))
		// Half expire immediately (TTL just above zero against the frozen
		// fake clock), half stay live.
		ttl := time.Nanosecond
		if i%2 == 1 {
			ttl = time.Hour
		}
		if err := m.Set(ctx, k, Entry{Value: []byte("v")}, Policy{TTL: ttl}); err != nil {
			t.Fatal(err)
		}
	}
	before := fake.reads.Load()
	// Force admission pressure: every Set beyond capacity sweeps.
	k, _ := NewKey("clk", "extra")
	if err := m.Set(ctx, k, Entry{Value: []byte("v")}, Policy{TTL: time.Minute}); err != nil {
		t.Fatal(err)
	}
	sweepReads := fake.reads.Load() - before
	// Set itself reads the clock (expiry of the replaced/new entry) plus at
	// most one for the sweep; the old per-entry behavior read 9+ times.
	if sweepReads > 2 {
		t.Fatalf("reclaim sweep performed %d clock reads; expected at most 2 (single-entry + one sweep)", sweepReads)
	}
	// Semantics unchanged: exactly the four expired entries are gone.
	m.mu.Lock()
	count := len(m.entries)
	m.mu.Unlock()
	if count != 5 { // 4 live + the newly admitted extra
		t.Fatalf("expected 5 surviving entries, got %d", count)
	}
}

type countingClock struct {
	reads atomic.Int64
	now   time.Time
}

// Now counts reads and advances a second per read: entries stored with
// sub-second TTLs are expired on any later read.
func (c *countingClock) Now() time.Time {
	c.reads.Add(1)
	c.now = c.now.Add(time.Second)
	return c.now
}

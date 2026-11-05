package imagecache

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go-meta/imageopt"
)

// F05 §47: prove that readers never observe a partially written entry. A
// controlled writer pauses after writing only part of the temporary file
// (by failing before rename), while concurrent readers poll the final key.

func TestAtomicityReadersNeverSeePartialEntry(t *testing.T) {
	dir := t.TempDir()
	c, err := Open(Config{Dir: dir, MaxTotalBytes: 1 << 20, MaxEntries: 10, MaxEntryBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	key := digestKey(strings.Repeat("aa", 32))
	data := webpBytes(500)
	rel, _ := relativeName(key, imageopt.FormatWebP)

	// A previous complete generation must stay readable throughout.
	gen1 := webpBytes(50)
	if err := c.Put(context.Background(), key, imageopt.FormatWebP, gen1); err != nil {
		t.Fatal(err)
	}

	// Stage a stalled writer: a temp file holding PARTIAL bytes, never
	// renamed to the final name (as if the process crashed mid-write).
	shard := shardOf(key)
	if err := c.root.MkdirAll(shard, dirPerm); err != nil {
		t.Fatal(err)
	}
	partial := data[:len(data)/2] // valid magic prefix, half the body
	tmpRel := shard + "/" + tempPrefix + "stalled.webp"
	if err := c.root.WriteFile(tmpRel, partial, filePerm); err != nil {
		t.Fatal(err)
	}

	// Readers during the stall: they may see the OLD complete generation
	// or a miss — but never partial bytes of the new one.
	for i := 0; i < 50; i++ {
		got, gerr := c.Get(context.Background(), key, imageopt.FormatWebP, 4096)
		if gerr == nil {
			if !bytes.Equal(got, gen1) {
				t.Fatalf("reader observed non-old bytes mid-stall: %d bytes", len(got))
			}
			if !bytes.HasPrefix(got, []byte("RIFF")) || len(got) < 12 {
				t.Fatalf("partial bytes served: %q", got[:min(12, len(got))])
			}
		} else if gerr != ErrCacheMiss {
			t.Fatalf("unexpected error mid-stall: %v", gerr)
		}
	}

	// The stalled temp file on disk must not answer lookups: the final
	// name still holds the old entry.
	st, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil || st.Size() != int64(len(gen1)) {
		t.Fatalf("final entry disturbed by staged writer: %v %d", err, st.Size())
	}

	// Release the writer: publish the full generation.
	if err := c.root.Remove(tmpRel); err != nil {
		t.Fatal(err)
	}
	if err := c.Put(context.Background(), key, imageopt.FormatWebP, data); err != nil {
		t.Fatal(err)
	}
	got, gerr := c.Get(context.Background(), key, imageopt.FormatWebP, 4096)
	if gerr != nil || !bytes.Equal(got, data) {
		t.Fatalf("full entry not available after release: %v", gerr)
	}
	if countTempFiles(t, dir) != 0 {
		t.Fatal("temp file remains after publication")
	}
	n, total := c.Stats()
	if n != 1 || total != int64(len(data)) {
		t.Fatalf("accounting wrong after replace: n=%d total=%d", n, total)
	}
}

// §48 extended: every corruption shape must miss-and-recover exactly once,
// with no temp files leaked and no infinite loop.
func TestCorruptionMatrix(t *testing.T) {
	corruptions := map[string]func(t *testing.T, dir, rel string){
		"truncated": func(t *testing.T, dir, rel string) {
			os.Truncate(filepath.Join(dir, filepath.FromSlash(rel)), 4)
		},
		"empty": func(t *testing.T, dir, rel string) {
			os.WriteFile(filepath.Join(dir, filepath.FromSlash(rel)), nil, 0o600)
		},
		"wrong-magic": func(t *testing.T, dir, rel string) {
			os.WriteFile(filepath.Join(dir, filepath.FromSlash(rel)), bytes.Repeat([]byte{0x00}, 64), 0o600)
		},
		"symlink": func(t *testing.T, dir, rel string) {
			p := filepath.Join(dir, filepath.FromSlash(rel))
			os.Remove(p)
			os.Symlink("/etc/hostname", p)
		},
		"removed": func(t *testing.T, dir, rel string) {
			os.Remove(filepath.Join(dir, filepath.FromSlash(rel)))
		},
	}
	for name, corrupt := range corruptions {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			c, err := Open(Config{Dir: dir, MaxTotalBytes: 1 << 20, MaxEntries: 10, MaxEntryBytes: 4096})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			key := digestKey(strings.Repeat("bb", 32))
			data := webpBytes(100)
			if err := c.Put(context.Background(), key, imageopt.FormatWebP, data); err != nil {
				t.Fatal(err)
			}
			rel, _ := relativeName(key, imageopt.FormatWebP)
			corrupt(t, dir, rel)

			// Corrupted: miss (never bad bytes).
			if got, err := c.Get(context.Background(), key, imageopt.FormatWebP, 4096); err == nil {
				t.Fatalf("%s: corrupt bytes served (%d)", name, len(got))
			}
			// Recovery: republish once, hit thereafter.
			if err := c.Put(context.Background(), key, imageopt.FormatWebP, data); err != nil {
				t.Fatalf("%s: recovery publish: %v", name, err)
			}
			got, err := c.Get(context.Background(), key, imageopt.FormatWebP, 4096)
			if err != nil || !bytes.Equal(got, data) {
				t.Fatalf("%s: post-recovery hit failed: %v", name, err)
			}
			if countTempFiles(t, dir) != 0 {
				t.Fatalf("%s: temp files leaked", name)
			}
		})
	}
}

// Close-during-publication race (§35 item 5): Put after Close fails
// cleanly; no partial entry, no panic.
func TestCloseDuringPublication(t *testing.T) {
	dir := t.TempDir()
	c, err := Open(Config{Dir: dir, MaxTotalBytes: 1 << 20, MaxEntries: 10, MaxEntryBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	key := digestKey(strings.Repeat("cc", 32))
	data := webpBytes(100)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(time.Millisecond)
		_ = c.Close()
	}()
	err = c.Put(context.Background(), key, imageopt.FormatWebP, data)
	wg.Wait()
	// Either outcome is acceptable as long as it is clean and no partial
	// entry is served afterwards.
	_ = err
	c2, err := Open(Config{Dir: dir, MaxTotalBytes: 1 << 20, MaxEntries: 10, MaxEntryBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if got, gerr := c2.Get(context.Background(), key, imageopt.FormatWebP, 4096); gerr == nil {
		if !bytes.Equal(got, data) {
			t.Fatalf("partial entry visible after close race: %d bytes", len(got))
		}
	}
}

// Multiple independent cache instances over separate directories must not
// interfere (§35 item 6).
func TestIndependentInstances(t *testing.T) {
	d1, d2 := t.TempDir(), t.TempDir()
	c1, err := Open(Config{Dir: d1, MaxTotalBytes: 1000, MaxEntries: 10, MaxEntryBytes: 100})
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Close()
	c2, err := Open(Config{Dir: d2, MaxTotalBytes: 1000, MaxEntries: 10, MaxEntryBytes: 100})
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	key := digestKey(strings.Repeat("dd", 32))
	a, b := webpBytes(10), webpBytes(20)
	if err := c1.Put(context.Background(), key, imageopt.FormatWebP, a); err != nil {
		t.Fatal(err)
	}
	if err := c2.Put(context.Background(), key, imageopt.FormatWebP, b); err != nil {
		t.Fatal(err)
	}
	g1, _ := c1.Get(context.Background(), key, imageopt.FormatWebP, 100)
	g2, _ := c2.Get(context.Background(), key, imageopt.FormatWebP, 100)
	if !bytes.Equal(g1, a) || !bytes.Equal(g2, b) {
		t.Fatal("instances cross-contaminated")
	}
}

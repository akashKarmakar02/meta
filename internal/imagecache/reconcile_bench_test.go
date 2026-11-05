package imagecache

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"go-meta/imageopt"
)

// Aliases keep the benchmark helper self-contained.
type osFileInfo = fs.FileInfo

var (
	osMkdirAll  = os.MkdirAll
	osReadFile  = os.ReadFile
	osWriteFile = os.WriteFile
)

// F06 §52: startup reconciliation cost as a function of existing entry
// count. Reconciliation must stay roughly linear in entries and cheap
// enough that restarts with a populated cache are not a deploy hazard.
// Populated-state setup happens outside the timed region; only Open (which
// reconciles) is measured.

// BenchmarkF06Reconciliation measures Open over caches pre-populated with
// N entries (plus, where noted, abandoned temp files and foreign files).
func BenchmarkF06Reconciliation(b *testing.B) {
	for _, n := range []int{0, 100, 500, 2000} {
		b.Run(fmt.Sprintf("entries=%d", n), func(b *testing.B) {
			base := b.TempDir()
			c, err := Open(Config{Dir: base, MaxTotalBytes: 1 << 30, MaxEntries: 1 << 20, MaxEntryBytes: 1 << 20})
			if err != nil {
				b.Fatal(err)
			}
			for i := 0; i < n; i++ {
				// Include the full counter as well as the shard; the test helper
				// wraps at 256 and silently collapses larger populations.
				key := fmt.Sprintf("v1-%02x%062x", i%256, i)
				if err := c.Put(context.Background(), key, imageopt.FormatWebP, webpBytes(64)); err != nil {
					b.Fatal(err)
				}
			}
			if got, _ := c.Stats(); got != n {
				b.Fatalf("populated %d entries, want %d", got, n)
			}
			// An abandoned temp file and a foreign file exercise the
			// non-entry paths of reconciliation too. With n=0 no shard
			// exists yet, so create one first.
			if err := c.root.MkdirAll("00", dirPerm); err != nil {
				b.Fatal(err)
			}
			if err := c.root.WriteFile("00/tmp-deadbeefdeadbeef.webp", []byte("junk"), filePerm); err != nil {
				b.Fatal(err)
			}
			if err := c.root.WriteFile("00/foreign-file.txt", []byte("operator data"), filePerm); err != nil {
				b.Fatal(err)
			}
			if err := c.Close(); err != nil {
				b.Fatal(err)
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				reopen := filepath.Join(b.TempDir(), fmt.Sprintf("reopen%d", i))
				// Copy the tree so every iteration reconciles identical state.
				if err := copyTree(base, reopen); err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
				c2, err := Open(Config{Dir: reopen, MaxTotalBytes: 1 << 30, MaxEntries: 1 << 20, MaxEntryBytes: 1 << 20})
				if err != nil {
					b.Fatal(err)
				}
				b.StopTimer()
				_ = c2.Close()
			}
		})
	}
}

// copyTree recursively copies a directory tree for benchmark setup.
func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info osFileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return osMkdirAll(target, info.Mode())
		}
		data, err := osReadFile(path)
		if err != nil {
			return err
		}
		return osWriteFile(target, data, info.Mode())
	})
}

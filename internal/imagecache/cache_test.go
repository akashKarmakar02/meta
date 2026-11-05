package imagecache

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go-meta/imageopt"
)

// Minimal valid encoded fixtures per format: real magic bytes so
// publication validation accepts them and hit verification sees them.

func webpBytes(n int) []byte {
	b := make([]byte, 12, 12+n)
	copy(b, "RIFF0000WEBP")
	for i := 0; i < n; i++ {
		b = append(b, byte('w'))
	}
	return b
}

func avifBytes(n int) []byte {
	b := make([]byte, 12, 12+n)
	b[0], b[1], b[2] = 0, 0, 0 // isom box size filled below
	copy(b[4:], "ftyp")
	copy(b[8:], "avif")
	for i := 0; i < n; i++ {
		b = append(b, byte('a'))
	}
	return b
}

func jpegBytes(n int) []byte {
	b := make([]byte, 2, 2+n)
	b[0], b[1] = 0xFF, 0xD8
	for i := 0; i < n; i++ {
		b = append(b, byte('j'))
	}
	return b
}

func pngBytes(n int) []byte {
	hdr := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}
	return append(hdr, bytes.Repeat([]byte{'p'}, n)...)
}

func fixture(f imageopt.Format, n int) []byte {
	switch f {
	case imageopt.FormatWebP:
		return webpBytes(n)
	case imageopt.FormatAVIF:
		return avifBytes(n)
	case imageopt.FormatJPEG:
		return jpegBytes(n)
	default:
		return pngBytes(n)
	}
}

// testCache opens a cache in t.TempDir() with workable bounds.
func testCache(t *testing.T, cfg Config) *Cache {
	t.Helper()
	if cfg.Dir == "" {
		cfg.Dir = t.TempDir()
	}
	c, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// mkKey builds a structurally valid key ("v1-" + 64 hex) with the given
// first two hex digits and a varying final digit.
func mkKey(shard string, salt int) string {
	body := []byte(strings.Repeat("0", 64))
	body[0], body[1] = shard[0], shard[1]
	const hexDigits = "0123456789abcdef"
	body[62] = hexDigits[(salt/16)%16]
	body[63] = hexDigits[salt%16]
	return "v1-" + string(body)
}

// keyDigest mimics a real digest-derived key with content in the hex body.
func digestKey(hex64 string) string { return "v1-" + hex64 }

// --- Constructor / configuration (§46 items 1–6) ---

func TestConfigValidation(t *testing.T) {
	base := Config{Dir: "/tmp/x", MaxTotalBytes: 1000, MaxEntries: 10, MaxEntryBytes: 100}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	for name, cfg := range map[string]Config{
		"empty dir":      {MaxTotalBytes: 1000, MaxEntries: 10, MaxEntryBytes: 100},
		"whitespace dir": {Dir: "  ", MaxTotalBytes: 1000, MaxEntries: 10, MaxEntryBytes: 100},
		"zero total":     {Dir: "/tmp/x", MaxEntries: 10, MaxEntryBytes: 100},
		"zero entries":   {Dir: "/tmp/x", MaxTotalBytes: 1000, MaxEntryBytes: 100},
		"zero entry":     {Dir: "/tmp/x", MaxTotalBytes: 1000, MaxEntries: 10},
		"entry>total":    {Dir: "/tmp/x", MaxTotalBytes: 100, MaxEntries: 10, MaxEntryBytes: 200},
	} {
		if err := cfg.Validate(); err == nil {
			t.Errorf("%s: accepted invalid config", name)
		}
	}
}

func TestOpenRejectsFileAsRoot(t *testing.T) {
	f := filepath.Join(t.TempDir(), "notadir")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(Config{Dir: f, MaxTotalBytes: 10, MaxEntries: 2, MaxEntryBytes: 10}); err == nil {
		t.Fatal("file as root accepted")
	}
}

func TestOpenRejectsSymlinkRoot(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	link := filepath.Join(dir, "link")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(Config{Dir: link, MaxTotalBytes: 10, MaxEntries: 2, MaxEntryBytes: 10}); err == nil {
		t.Fatal("symlink root accepted")
	}
}

func TestOpenRejectsReadOnlyDir(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root: permissions do not apply")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	_, err := Open(Config{Dir: dir, MaxTotalBytes: 10, MaxEntries: 2, MaxEntryBytes: 10})
	if err == nil {
		t.Fatal("read-only dir accepted")
	}
	if !strings.Contains(err.Error(), "not writable") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- Hit / miss / identity (items 7–15) ---

func TestPutGetRoundTrip(t *testing.T) {
	c := testCache(t, Config{MaxTotalBytes: 1 << 20, MaxEntries: 100, MaxEntryBytes: 4096})
	key := digestKey(strings.Repeat("ab", 32))
	data := webpBytes(100)
	if err := c.Put(context.Background(), key, imageopt.FormatWebP, data); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := c.Get(context.Background(), key, imageopt.FormatWebP, 4096)
	if err != nil {
		t.Fatalf("Get after Put: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("round trip mismatch")
	}
	if n, _ := c.Stats(); n != 1 {
		t.Fatalf("entries = %d", n)
	}
}

func TestMissForUnknownKey(t *testing.T) {
	c := testCache(t, Config{MaxTotalBytes: 1 << 20, MaxEntries: 10, MaxEntryBytes: 4096})
	if _, err := c.Get(context.Background(), digestKey(strings.Repeat("cd", 32)), imageopt.FormatWebP, 4096); err != ErrCacheMiss {
		t.Fatalf("want ErrCacheMiss, got %v", err)
	}
}

func TestDifferentKeysDoNotCollide(t *testing.T) {
	c := testCache(t, Config{MaxTotalBytes: 1 << 20, MaxEntries: 100, MaxEntryBytes: 4096})
	k1 := digestKey(strings.Repeat("aa", 32))
	k2 := digestKey(strings.Repeat("bb", 32))
	if err := c.Put(context.Background(), k1, imageopt.FormatWebP, webpBytes(10)); err != nil {
		t.Fatal(err)
	}
	if err := c.Put(context.Background(), k2, imageopt.FormatWebP, webpBytes(20)); err != nil {
		t.Fatal(err)
	}
	g1, _ := c.Get(context.Background(), k1, imageopt.FormatWebP, 4096)
	g2, _ := c.Get(context.Background(), k2, imageopt.FormatWebP, 4096)
	if len(g1) != 22 || len(g2) != 32 {
		t.Fatalf("cross-key collision: %d vs %d", len(g1), len(g2))
	}
}

func TestFormatExtensionDistinguishesEntries(t *testing.T) {
	c := testCache(t, Config{MaxTotalBytes: 1 << 20, MaxEntries: 100, MaxEntryBytes: 4096})
	key := digestKey(strings.Repeat("ee", 32))
	if err := c.Put(context.Background(), key, imageopt.FormatWebP, webpBytes(5)); err != nil {
		t.Fatal(err)
	}
	// Same key, different format: separate file, and Get with the other
	// format misses (the stored .webp does not answer an .avif lookup).
	if _, err := c.Get(context.Background(), key, imageopt.FormatAVIF, 4096); err != ErrCacheMiss {
		t.Fatalf("webp entry answered avif lookup: %v", err)
	}
	if err := c.Put(context.Background(), key, imageopt.FormatAVIF, avifBytes(5)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(context.Background(), key, imageopt.FormatWebP, 4096); err != nil {
		t.Fatalf("webp entry lost after avif publication: %v", err)
	}
}

// --- Publication / atomicity (§46 items 16–19, §47) ---

func TestPutRejectsInvalidInput(t *testing.T) {
	c := testCache(t, Config{MaxTotalBytes: 1 << 20, MaxEntries: 10, MaxEntryBytes: 4096})
	key := digestKey(strings.Repeat("11", 32))
	if err := c.Put(context.Background(), key, imageopt.FormatWebP, nil); err == nil {
		t.Error("empty data accepted")
	}
	if err := c.Put(context.Background(), key, imageopt.FormatWebP, pngBytes(10)); err == nil {
		t.Error("wrong-magic data accepted")
	}
	if err := c.Put(context.Background(), "badkey", imageopt.FormatWebP, webpBytes(4)); err == nil {
		t.Error("invalid key accepted")
	}
	if err := c.Put(context.Background(), key, imageopt.FormatAuto, webpBytes(4)); err == nil {
		t.Error("auto format accepted")
	}
	if err := c.Put(context.Background(), key, imageopt.FormatWebP, webpBytes(5000)); err == nil {
		t.Error("oversize entry accepted")
	}
	if _, err := c.Get(context.Background(), key, imageopt.FormatWebP, 4096); err != ErrCacheMiss {
		t.Errorf("rejected puts must not publish: %v", err)
	}
}

func TestNoTempFilesAfterPublish(t *testing.T) {
	dir := t.TempDir()
	c := testCache(t, Config{Dir: dir, MaxTotalBytes: 1 << 20, MaxEntries: 10, MaxEntryBytes: 4096})
	key := digestKey(strings.Repeat("22", 32))
	if err := c.Put(context.Background(), key, imageopt.FormatWebP, webpBytes(30)); err != nil {
		t.Fatal(err)
	}
	leftover := countTempFiles(t, dir)
	if leftover != 0 {
		t.Fatalf("%d temp files left behind", leftover)
	}
}

func countTempFiles(t *testing.T, root string) int {
	t.Helper()
	n := 0
	steps := append([]string{root}, shardDirs(root)...)
	for _, d := range steps {
		ents, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range ents {
			if !e.IsDir() && strings.HasPrefix(e.Name(), tempPrefix+".") {
				n++
			}
		}
	}
	return n
}

func shardDirs(root string) []string {
	out := make([]string, 0, 256)
	for i := 0; i < 256; i++ {
		out = append(out, filepath.Join(root, fmt.Sprintf("%02x", i)))
	}
	return out
}

// --- Corruption / recovery (§48) ---

func TestCorruptTruncatedEntryRecovers(t *testing.T) {
	dir := t.TempDir()
	c := testCache(t, Config{Dir: dir, MaxTotalBytes: 1 << 20, MaxEntries: 10, MaxEntryBytes: 4096})
	key := digestKey(strings.Repeat("33", 32))
	data := webpBytes(200)
	if err := c.Put(context.Background(), key, imageopt.FormatWebP, data); err != nil {
		t.Fatal(err)
	}
	// Truncate on disk behind the cache's back.
	rel, err := relativeName(key, imageopt.FormatWebP)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.Truncate(p, 5); err != nil {
		t.Fatal(err)
	}
	// Get detects (size shrank past the RIFF structure → magic fails or
	// length mismatch), removes, misses.
	if _, err := c.Get(context.Background(), key, imageopt.FormatWebP, 4096); err != ErrCacheMiss {
		t.Fatalf("truncated entry served: %v", err)
	}
	if _, serr := os.Stat(p); !os.IsNotExist(serr) {
		t.Fatal("corrupt entry not removed")
	}
	// Republish fresh: subsequent hits work.
	if err := c.Put(context.Background(), key, imageopt.FormatWebP, data); err != nil {
		t.Fatal(err)
	}
	got, err := c.Get(context.Background(), key, imageopt.FormatWebP, 4096)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("recovery failed: %v", err)
	}
}

func TestCorruptWrongMagicRecovers(t *testing.T) {
	dir := t.TempDir()
	c := testCache(t, Config{Dir: dir, MaxTotalBytes: 1 << 20, MaxEntries: 10, MaxEntryBytes: 4096})
	key := digestKey(strings.Repeat("44", 32))
	if err := c.Put(context.Background(), key, imageopt.FormatWebP, webpBytes(50)); err != nil {
		t.Fatal(err)
	}
	rel, _ := relativeName(key, imageopt.FormatWebP)
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.WriteFile(p, bytes.Repeat([]byte("X"), 62), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(context.Background(), key, imageopt.FormatWebP, 4096); err != ErrCacheMiss {
		t.Fatalf("garbage served: %v", err)
	}
	if _, serr := os.Stat(p); !os.IsNotExist(serr) {
		t.Fatal("corrupt entry not removed")
	}
}

func TestOversizedEntryOnHitRejectedAndRemoved(t *testing.T) {
	dir := t.TempDir()
	c := testCache(t, Config{Dir: dir, MaxTotalBytes: 1 << 20, MaxEntries: 10, MaxEntryBytes: 4096})
	key := digestKey(strings.Repeat("55", 32))
	if err := c.Put(context.Background(), key, imageopt.FormatWebP, webpBytes(50)); err != nil {
		t.Fatal(err)
	}
	// Hit with a tighter bound than publication had: the entry is too big
	// for this caller and is removed rather than served.
	if _, err := c.Get(context.Background(), key, imageopt.FormatWebP, 20); err != ErrCacheMiss {
		t.Fatalf("oversized entry served: %v", err)
	}
}

func TestSymlinkEntryNeverServed(t *testing.T) {
	dir := t.TempDir()
	c := testCache(t, Config{Dir: dir, MaxTotalBytes: 1 << 20, MaxEntries: 10, MaxEntryBytes: 4096})
	key := digestKey(strings.Repeat("66", 32))
	if err := c.Put(context.Background(), key, imageopt.FormatWebP, webpBytes(50)); err != nil {
		t.Fatal(err)
	}
	rel, _ := relativeName(key, imageopt.FormatWebP)
	p := filepath.Join(dir, filepath.FromSlash(rel))
	secret := filepath.Join(dir, "secret")
	os.WriteFile(secret, webpBytes(999), 0o600)
	os.Remove(p)
	if err := os.Symlink(secret, p); err != nil {
		t.Fatal(err)
	}
	if got, err := c.Get(context.Background(), key, imageopt.FormatWebP, 4096); err == nil {
		t.Fatalf("symlink followed, got %d bytes", len(got))
	}
}

// --- Startup reconciliation (§31, §46 item 29–30, 37) ---

func TestStartupReconciliationRebuildsAccounting(t *testing.T) {
	dir := t.TempDir()
	c := testCache(t, Config{Dir: dir, MaxTotalBytes: 1 << 20, MaxEntries: 100, MaxEntryBytes: 4096})
	keys := []string{digestKey(strings.Repeat("77", 32)), digestKey(strings.Repeat("88", 32))}
	for i, k := range keys {
		if err := c.Put(context.Background(), k, imageopt.FormatWebP, webpBytes(10*(i+1))); err != nil {
			t.Fatal(err)
		}
	}
	n, total := c.Stats()
	_ = c.Close()

	// Foreign file directly in a shard: ignored by reconciliation.
	foreign := filepath.Join(dir, shardOf(keys[0]), "foreign.txt")
	os.WriteFile(foreign, []byte("operator data"), 0o600)

	c2, err := Open(Config{Dir: dir, MaxTotalBytes: 1 << 20, MaxEntries: 100, MaxEntryBytes: 4096})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer c2.Close()
	n2, total2 := c2.Stats()
	if n2 != n || total2 != total {
		t.Fatalf("reconciled accounting %d/%d want %d/%d", n2, total2, n, total)
	}
	for _, k := range keys {
		if _, err := c2.Get(context.Background(), k, imageopt.FormatWebP, 4096); err != nil {
			t.Fatalf("entry lost across restart: %v", err)
		}
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatalf("foreign file deleted by reconciliation: %v", err)
	}
}

func TestStartupRemovesAbandonedTempFiles(t *testing.T) {
	dir := t.TempDir()
	c := testCache(t, Config{Dir: dir, MaxTotalBytes: 1 << 20, MaxEntries: 10, MaxEntryBytes: 4096})
	key := digestKey(strings.Repeat("99", 32))
	if err := c.Put(context.Background(), key, imageopt.FormatWebP, webpBytes(10)); err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	// Simulate a crashed writer: a leftover temp file in a shard.
	shard := key[:2]
	if err := os.MkdirAll(filepath.Join(dir, shard), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, shard, tempPrefix+".deadbeef.webp"), webpBytes(500), 0o600); err != nil {
		t.Fatal(err)
	}
	c2, err := Open(Config{Dir: dir, MaxTotalBytes: 1 << 20, MaxEntries: 10, MaxEntryBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if countTempFiles(t, dir) != 0 {
		t.Fatal("abandoned temp file not reclaimed")
	}
	if _, err := c2.Get(context.Background(), key, imageopt.FormatWebP, 4096); err != nil {
		t.Fatalf("valid entry lost during temp cleanup: %v", err)
	}
}

// --- Bounded storage / eviction (§28–30, §49) ---

func TestEvictionKeepsBoundsByteBudget(t *testing.T) {
	c := testCache(t, Config{MaxTotalBytes: 400, MaxEntries: 1000, MaxEntryBytes: 150})
	// Entries of 112+12=... webpBytes(100) = 112 bytes each.
	var lastErrKey string
	for i := 0; i < 20; i++ {
		k := mkKey(fmt.Sprintf("%02x", i%256), i)
		lastErrKey = k
		if err := c.Put(context.Background(), k, imageopt.FormatWebP, webpBytes(100)); err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
	}
	n, total := c.Stats()
	if total > 400 {
		t.Fatalf("byte budget exceeded: %d", total)
	}
	if n > 3 { // 400/112 = 3 full entries
		t.Fatalf("entry count above byte-budget share: %d", n)
	}
	if _, err := c.Get(context.Background(), lastErrKey, imageopt.FormatWebP, 4096); err != nil {
		t.Fatalf("newest entry evicted first: %v", err)
	}
}

func TestEvictionKeepsBoundsEntryCount(t *testing.T) {
	c := testCache(t, Config{MaxTotalBytes: 1 << 20, MaxEntries: 3, MaxEntryBytes: 4096})
	keys := []string{}
	for i := 0; i < 5; i++ {
		k := mkKey(fmt.Sprintf("%02x", i%256), i)
		keys = append(keys, k)
		if err := c.Put(context.Background(), k, imageopt.FormatWebP, webpBytes(20)); err != nil {
			t.Fatal(err)
		}
	}
	n, _ := c.Stats()
	if n != 3 {
		t.Fatalf("entries = %d, want 3", n)
	}
	// Newest three survive.
	for _, k := range keys[2:] {
		if _, err := c.Get(context.Background(), k, imageopt.FormatWebP, 4096); err != nil {
			t.Fatalf("recent entry evicted: %v", err)
		}
	}
	// Oldest two evicted.
	for _, k := range keys[:2] {
		if _, err := c.Get(context.Background(), k, imageopt.FormatWebP, 4096); err != ErrCacheMiss {
			t.Fatalf("old entry survived: %v", err)
		}
	}
}

func TestNoUnrelatedFileDeletion(t *testing.T) {
	dir := t.TempDir()
	c := testCache(t, Config{Dir: dir, MaxTotalBytes: 300, MaxEntries: 1000, MaxEntryBytes: 200})
	foreign := filepath.Join(dir, "README-operator.txt")
	if err := os.WriteFile(foreign, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		k := mkKey(fmt.Sprintf("%02x", i%256), i)
		if err := c.Put(context.Background(), k, imageopt.FormatWebP, webpBytes(100)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatalf("unrelated file deleted: %v", err)
	}
}

// --- Concurrency (§33–35) ---

func TestConcurrentSameKeyPublishIsSafe(t *testing.T) {
	c := testCache(t, Config{MaxTotalBytes: 1 << 20, MaxEntries: 100, MaxEntryBytes: 4096})
	key := digestKey(strings.Repeat("aa", 32))
	const writers = 8
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			d := webpBytes(10 + i)
			if err := c.Put(context.Background(), key, imageopt.FormatWebP, d); err != nil {
				t.Errorf("writer %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	got, err := c.Get(context.Background(), key, imageopt.FormatWebP, 4096)
	if err != nil {
		t.Fatalf("no entry after concurrent put: %v", err)
	}
	// The winner is one of the complete inputs — never interleaved bytes.
	if len(got) < 12 || !bytes.HasPrefix(got, []byte("RIFF")) {
		t.Fatalf("corrupt merged entry: %q", got[:min(20, len(got))])
	}
	if n, _ := c.Stats(); n != 1 {
		t.Fatalf("double-counted key: %d entries", n)
	}
}

func TestConcurrentDistinctKeysProgress(t *testing.T) {
	c := testCache(t, Config{MaxTotalBytes: 1 << 20, MaxEntries: 500, MaxEntryBytes: 4096})
	const n = 64
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			k := mkKey(fmt.Sprintf("%02x", i%256), i)
			d := webpBytes(i + 1)
			if err := c.Put(context.Background(), k, imageopt.FormatWebP, d); err != nil {
				t.Errorf("put %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	got, total := c.Stats()
	if got != n {
		t.Fatalf("entries = %d, want %d", got, n)
	}
	var want int64
	for i := 1; i <= n; i++ {
		want += int64(12 + i)
	}
	if total != want {
		t.Fatalf("accounting = %d, want %d", total, want)
	}
}

func TestReaderWhileEvictionAndPublish(t *testing.T) {
	c := testCache(t, Config{MaxTotalBytes: 300, MaxEntries: 1000, MaxEntryBytes: 250})
	// Fill so every new publication evicts.
	for i := 0; i < 6; i++ {
		k := mkKey(fmt.Sprintf("%02x", i%256), i)
		if err := c.Put(context.Background(), k, imageopt.FormatWebP, webpBytes(50)); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func(r int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				k := mkKey(fmt.Sprintf("%02x", r%256), r)
				b, err := c.Get(context.Background(), k, imageopt.FormatWebP, 4096)
				if err == nil && (!bytes.HasPrefix(b, []byte("RIFF")) || len(b) < 12) {
					t.Errorf("partial bytes served: %q", b)
					return
				}
			}
		}(r)
	}
	for i := 6; i < 60; i++ {
		k := mkKey(fmt.Sprintf("%02x", i%256), i)
		if err := c.Put(context.Background(), k, imageopt.FormatWebP, webpBytes(100)); err != nil {
			t.Error(err)
		}
	}
	close(stop)
	wg.Wait()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// --- Close / cancellation (§46 items 21–22) ---

func TestClosedCacheRefusesOps(t *testing.T) {
	dir := t.TempDir()
	c, err := Open(Config{Dir: dir, MaxTotalBytes: 1000, MaxEntries: 10, MaxEntryBytes: 100})
	if err != nil {
		t.Fatal(err)
	}
	key := digestKey(strings.Repeat("bb", 32))
	if err := c.Put(context.Background(), key, imageopt.FormatWebP, webpBytes(10)); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("double close not idempotent: %v", err)
	}
	if _, err := c.Get(context.Background(), key, imageopt.FormatWebP, 100); err != ErrCacheClosed {
		t.Fatalf("Get after Close: %v", err)
	}
	if err := c.Put(context.Background(), key, imageopt.FormatWebP, webpBytes(10)); err != ErrCacheClosed {
		t.Fatalf("Put after Close: %v", err)
	}
}

func TestCanceledContext(t *testing.T) {
	c := testCache(t, Config{MaxTotalBytes: 1000, MaxEntries: 10, MaxEntryBytes: 100})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	key := digestKey(strings.Repeat("cc", 32))
	if err := c.Put(ctx, key, imageopt.FormatWebP, webpBytes(10)); err == nil {
		t.Fatal("canceled Put accepted")
	}
	if _, err := c.Get(ctx, key, imageopt.FormatWebP, 100); err == nil {
		t.Fatal("canceled Get accepted")
	}
}

// --- Filename safety (§16, §46 item 32) ---

func TestKeyValidationRejectsTraversal(t *testing.T) {
	for _, k := range []string{
		"", "v1-", "v1-abc", "v1-" + strings.Repeat("AB", 32),
		"v1-" + strings.Repeat("0z", 32), "../v1-" + strings.Repeat("00", 30),
		"v1-" + strings.Repeat("00", 31) + "/x", "v1-\x00" + strings.Repeat("00", 30) + "AAAA",
	} {
		if err := validKey(k); err == nil {
			t.Errorf("invalid key accepted: %q", k)
		}
		if _, err := relativeName(k, imageopt.FormatWebP); err == nil {
			t.Errorf("path derived from invalid key: %q", k)
		}
	}
}

// --- High-cardinality churn (§49) ---

func TestBoundedChurn(t *testing.T) {
	c := testCache(t, Config{MaxTotalBytes: 4096, MaxEntries: 64, MaxEntryBytes: 512})
	const iterations = 3000
	for i := 0; i < iterations; i++ {
		k := mkKey(fmt.Sprintf("%02x", i%256), (i % 251))
		d := webpBytes(1 + i%37)
		if err := c.Put(context.Background(), k, imageopt.FormatWebP, d); err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		if i%97 == 0 {
			mk := mkKey(fmt.Sprintf("%02x", (i*7)%256), ((i * 13) % 251))
			c.Get(context.Background(), mk, imageopt.FormatWebP, 512) // mixed hits, any result fine
		}
	}
	n, total := c.Stats()
	if n > 64 {
		t.Fatalf("entries unbounded: %d", n)
	}
	if total > 4096 {
		t.Fatalf("bytes unbounded: %d", total)
	}
}

// --- Recency bump behavior ---

func TestRecencyBumpKeepsHotEntry(t *testing.T) {
	dir := t.TempDir()
	c := testCache(t, Config{Dir: dir, MaxTotalBytes: 300, MaxEntries: 100, MaxEntryBytes: 250})
	hot := mkKey("01", 1)
	if err := c.Put(context.Background(), hot, imageopt.FormatWebP, webpBytes(50)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	cold := digestKey("02" + strings.Repeat("0", 62))
	if err := c.Put(context.Background(), cold, imageopt.FormatWebP, webpBytes(50)); err != nil {
		t.Fatal(err)
	}
	// Touch hot so it stays newest.
	if _, err := c.Get(context.Background(), hot, imageopt.FormatWebP, 4096); err != nil {
		t.Fatal(err)
	}
	// This publication exceeds the budget; older (cold) must go first.
	third := digestKey("03" + strings.Repeat("0", 62))
	if err := c.Put(context.Background(), third, imageopt.FormatWebP, webpBytes(200)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(context.Background(), hot, imageopt.FormatWebP, 4096); err != nil {
		t.Fatalf("hot entry evicted before cold: %v", err)
	}
}

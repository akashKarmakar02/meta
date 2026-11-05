package imagecache

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"go-meta/imageopt"
)

// Cancel at the second context check, after the temporary file was written.
type cancelAtPublication struct {
	context.Context
	checks atomic.Int32
}

func (c *cancelAtPublication) Err() error {
	if c.checks.Add(1) >= 2 {
		return context.Canceled
	}
	return nil
}
func TestF06CancellationBeforeRenameRemovesTemp(t *testing.T) {
	dir := t.TempDir()
	c, err := Open(Config{Dir: dir, MaxTotalBytes: 1 << 20, MaxEntries: 10, MaxEntryBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := &cancelAtPublication{Context: context.Background()}
	key := digestKey(strings.Repeat("ab", 32))
	if err := c.Put(ctx, key, imageopt.FormatWebP, webpBytes(100)); !errors.Is(err, context.Canceled) {
		t.Fatalf("put returned %v", err)
	}
	if n, _ := c.Stats(); n != 0 {
		t.Fatalf("published %d entries", n)
	}
	if n := countTempFiles(t, dir); n != 0 {
		t.Fatalf("left %d temporary files", n)
	}
}

// This pins the operator-trusted cache boundary: format magic is not an
// integrity or authenticity check over image payload bytes.
func TestF06ValidMagicDoesNotProveIntegrity(t *testing.T) {
	dir := t.TempDir()
	c, err := Open(Config{Dir: dir, MaxTotalBytes: 1 << 20, MaxEntries: 10, MaxEntryBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	key := digestKey(strings.Repeat("cd", 32))
	data := webpBytes(100)
	if err := c.Put(context.Background(), key, imageopt.FormatWebP, data); err != nil {
		t.Fatal(err)
	}
	rel, err := relativeName(key, imageopt.FormatWebP)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 0xff
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(rel)), data, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := c.Get(context.Background(), key, imageopt.FormatWebP, 4096)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("expected documented magic-only behavior, got %v", err)
	}
}

func TestF06SymlinkToInternalEntryIsNotServed(t *testing.T) {
	dir := t.TempDir()
	c, err := Open(Config{Dir: dir, MaxTotalBytes: 1 << 20, MaxEntries: 10, MaxEntryBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	first := digestKey(strings.Repeat("aa", 32))
	second := digestKey(strings.Repeat("bb", 32))
	for _, key := range []string{first, second} {
		if err := c.Put(context.Background(), key, imageopt.FormatWebP, webpBytes(32)); err != nil {
			t.Fatal(err)
		}
	}
	a, _ := relativeName(first, imageopt.FormatWebP)
	b, _ := relativeName(second, imageopt.FormatWebP)
	if err := os.Remove(filepath.Join(dir, filepath.FromSlash(a))); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", filepath.FromSlash(b)), filepath.Join(dir, filepath.FromSlash(a))); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(context.Background(), first, imageopt.FormatWebP, 4096); !errors.Is(err, ErrCacheMiss) {
		t.Fatalf("symlink entry served: %v", err)
	}
	if _, err := c.Get(context.Background(), second, imageopt.FormatWebP, 4096); err != nil {
		t.Fatalf("target damaged: %v", err)
	}
}
func TestF06StaleCorruptReaderCannotDeleteReplacement(t *testing.T) {
	dir := t.TempDir()
	c, err := Open(Config{Dir: dir, MaxTotalBytes: 1 << 20, MaxEntries: 10, MaxEntryBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	key := digestKey(strings.Repeat("ef", 32))
	first := webpBytes(20)
	second := webpBytes(30)
	if err := c.Put(context.Background(), key, imageopt.FormatWebP, first); err != nil {
		t.Fatal(err)
	}
	rel, _ := relativeName(key, imageopt.FormatWebP)
	f, err := c.root.Open(rel)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Put(context.Background(), key, imageopt.FormatWebP, second); err != nil {
		t.Fatal(err)
	}
	c.removeBadEntry(rel, st)
	got, err := c.Get(context.Background(), key, imageopt.FormatWebP, 4096)
	if err != nil || !bytes.Equal(got, second) {
		t.Fatalf("new entry removed by stale reader: %v", err)
	}
}

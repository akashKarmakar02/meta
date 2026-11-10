package cache_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/akashKarmakar02/meta/cache"
)

func newDiskStore(t *testing.T) (*cache.Disk, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := cache.NewDiskStore(cache.DiskConfig{Dir: dir})
	if err != nil {
		t.Fatalf("NewDisk: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, dir
}

func TestDiskSetGetRoundTrip(t *testing.T) {
	store, _ := newDiskStore(t)
	ctx := context.Background()
	key, errk := cache.NewKey("test", "alpha")
	if errk != nil {
		t.Fatalf("NewKey: %v", errk)
	}
	entry := cache.Entry{Value: []byte("hello world")}
	if err := store.Set(ctx, key, entry, cache.Policy{TTL: time.Minute}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, ok, err := store.Get(ctx, key)
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if string(got.Value) != "hello world" {
		t.Fatalf("value = %q", got.Value)
	}
}

func TestDiskPersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	key, errk := cache.NewKey("test", "persist")
	if errk != nil {
		t.Fatalf("NewKey: %v", errk)
	}
	entry := cache.Entry{Value: []byte("durable")}

	s1, err := cache.NewDiskStore(cache.DiskConfig{Dir: dir})
	if err != nil {
		t.Fatalf("NewDisk: %v", err)
	}
	if err := s1.Set(context.Background(), key, entry, cache.Policy{TTL: time.Hour}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2, err := cache.NewDiskStore(cache.DiskConfig{Dir: dir})
	if err != nil {
		t.Fatalf("reopen NewDisk: %v", err)
	}
	defer s2.Close()
	got, ok, err := s2.Get(context.Background(), key)
	if err != nil || !ok {
		t.Fatalf("Get after reopen: ok=%v err=%v", ok, err)
	}
	if string(got.Value) != "durable" {
		t.Fatalf("value = %q", got.Value)
	}
}

func TestDiskDelete(t *testing.T) {
	store, _ := newDiskStore(t)
	ctx := context.Background()
	key, errk := cache.NewKey("test", "gone")
	if errk != nil {
		t.Fatalf("NewKey: %v", errk)
	}
	if err := store.Set(ctx, key, cache.Entry{Value: []byte("x")}, cache.Policy{TTL: time.Minute}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := store.Delete(ctx, key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok, _ := store.Get(ctx, key); ok {
		t.Fatal("entry survived Delete")
	}
}

func TestDiskExpiry(t *testing.T) {
	store, _ := newDiskStore(t)
	ctx := context.Background()
	key, errk := cache.NewKey("test", "expire")
	if errk != nil {
		t.Fatalf("NewKey: %v", errk)
	}
	if err := store.Set(ctx, key, cache.Entry{Value: []byte("soon stale")}, cache.Policy{TTL: 30 * time.Millisecond}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	time.Sleep(60 * time.Millisecond)
	if _, ok, err := store.Get(ctx, key); ok || err != nil {
		t.Fatalf("expired entry returned: ok=%v err=%v", ok, err)
	}
}

func TestDiskClosedThenUsed(t *testing.T) {
	store, _ := newDiskStore(t)
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	ctx := context.Background()
	key, _ := cache.NewKey("test", "closed")
	if _, _, err := store.Get(ctx, key); !errors.Is(err, cache.ErrClosed) {
		t.Fatalf("Get after Close = %v, want ErrClosed", err)
	}
	if err := store.Set(ctx, key, cache.Entry{Value: []byte("x")}, cache.Policy{TTL: time.Minute}); !errors.Is(err, cache.ErrClosed) {
		t.Fatalf("Set after Close = %v, want ErrClosed", err)
	}
}

func TestDiskNilContextRejected(t *testing.T) {
	store, _ := newDiskStore(t)
	key, _ := cache.NewKey("test", "nilctx")
	//nolint:staticcheck // deliberately testing nil context handling
	err := store.Set(nil, key, cache.Entry{Value: []byte("x")}, cache.Policy{TTL: time.Minute})
	if !errors.Is(err, cache.ErrNilContext) {
		t.Fatalf("Set with nil ctx = %v, want ErrNilContext", err)
	}
}

func TestDiskOversizedEntryRejected(t *testing.T) {
	dir := t.TempDir()
	store, err := cache.NewDiskStore(cache.DiskConfig{Dir: dir, MaxEntryBytes: 16})
	if err != nil {
		t.Fatalf("NewDisk: %v", err)
	}
	defer store.Close()
	key, _ := cache.NewKey("test", "big")
	err = store.Set(context.Background(), key, cache.Entry{Value: make([]byte, 64)}, cache.Policy{TTL: time.Minute})
	if !errors.Is(err, cache.ErrEntryTooLarge) {
		t.Fatalf("oversized Set = %v, want ErrEntryTooLarge", err)
	}
}

func TestDiskCorruptFileIsMiss(t *testing.T) {
	store, dir := newDiskStore(t)
	ctx := context.Background()
	key, _ := cache.NewKey("test", "corrupt")
	if err := store.Set(ctx, key, cache.Entry{Value: []byte("good")}, cache.Policy{TTL: time.Hour}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Corrupt every cache data file in the dir.
	err := filepath.Walk(dir, func(p string, fi os.FileInfo, _ error) error {
		if fi != nil && !fi.IsDir() && strings.HasSuffix(p, ".bin") {
			return os.WriteFile(p, []byte("garbage"), 0o644)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	s2, err := cache.NewDiskStore(cache.DiskConfig{Dir: dir})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	if _, ok, err := s2.Get(ctx, key); ok || err != nil {
		t.Fatalf("corrupt entry surfaced: ok=%v err=%v", ok, err)
	}
}

func TestDiskConcurrentAccess(t *testing.T) {
	store, _ := newDiskStore(t)
	ctx := context.Background()
	const workers = 8
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				k, kerr := cache.NewKey("test", fmt.Sprintf("w%d-i%d", w, i))
				if kerr != nil {
					t.Errorf("NewKey: %v", kerr)
					return
				}
				if err := store.Set(ctx, k, cache.Entry{Value: []byte("v")}, cache.Policy{TTL: time.Minute}); err != nil {
					t.Errorf("Set: %v", err)
					return
				}
				if _, ok, err := store.Get(ctx, k); !ok || err != nil {
					t.Errorf("Get: ok=%v err=%v", ok, err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
}

func TestDiskNeverTTL(t *testing.T) {
	store, dir := newDiskStore(t)
	ctx := context.Background()

	k, err := cache.NewKey("test", "never")
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}
	if err := store.Set(ctx, k, cache.Entry{Value: []byte("forever")}, cache.Policy{TTL: cache.Never}); err != nil {
		t.Fatalf("Set with Never: %v", err)
	}

	// Reload from disk: a Never entry must still be present.
	store2, err := cache.NewDiskStore(cache.DiskConfig{Dir: dir})
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	entry, ok, err := store2.Get(ctx, k)
	if !ok || err != nil {
		t.Fatalf("Never entry lost on reload: ok=%v err=%v", ok, err)
	}
	if string(entry.Value) != "forever" {
		t.Fatalf("value = %q, want %q", entry.Value, "forever")
	}
}

func TestMemoryNeverTTL(t *testing.T) {
	m, err := cache.NewMemory(cache.MemoryConfig{MaxEntries: 10, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	k, err := cache.NewKey("test", "never")
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}
	ctx := context.Background()
	if err := m.Set(ctx, k, cache.Entry{Value: []byte("v")}, cache.Policy{TTL: cache.Never}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if _, ok, err := m.Get(ctx, k); !ok || err != nil {
		t.Fatalf("Never entry expired immediately: ok=%v err=%v", ok, err)
	}
}

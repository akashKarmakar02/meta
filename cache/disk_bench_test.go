package cache

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func BenchmarkDiskSet(b *testing.B) {
	store := newDiskBenchStore(b)
	ctx := context.Background()
	policy := Policy{TTL: time.Minute}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := store.Set(ctx, mustBenchKey(b, "ns", "bench", fmt.Sprintf("k%d", i)), Entry{Value: []byte("benchmark value")}, policy); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDiskGetHit(b *testing.B) {
	store := newDiskBenchStore(b)
	ctx := context.Background()
	policy := Policy{TTL: time.Minute}
	key := mustBenchKey(b, "ns", "bench", "fixed")
	if err := store.Set(ctx, key, Entry{Value: []byte("benchmark value")}, policy); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok, err := store.Get(ctx, key); err != nil || !ok {
			b.Fatal(err)
		}
	}
}

func BenchmarkDiskGetMiss(b *testing.B) {
	store := newDiskBenchStore(b)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok, err := store.Get(ctx, mustBenchKey(b, "ns", "bench", fmt.Sprintf("missing%d", i))); err != nil || ok {
			b.Fatal(err)
		}
	}
}

func newDiskBenchStore(b *testing.B) *Disk {
	b.Helper()
	dir := b.TempDir()
	store, err := NewDiskStore(DiskConfig{Dir: dir})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	return store
}

func mustBenchKey(b *testing.B, namespace string, components ...string) Key {
	b.Helper()
	key, err := NewKey(namespace, components...)
	if err != nil {
		b.Fatal(err)
	}
	return key
}

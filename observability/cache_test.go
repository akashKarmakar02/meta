package observability

import (
	"context"
	"testing"
	"time"

	"github.com/akashKarmakar02/meta/cache"
)

func TestWrapCacheReportsOutcomes(t *testing.T) {
	memory, err := cache.NewMemory(cache.MemoryConfig{MaxEntries: 16, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer memory.Close()
	collector := NewCollector()
	ctx := context.WithValue(context.Background(), contextKey{}, &request{id: "test", metrics: collector})
	store := WrapCache(memory)
	key := cache.Key("private-key")
	if _, hit, err := store.Get(ctx, key); err != nil || hit {
		t.Fatalf("miss hit=%v err=%v", hit, err)
	}
	if err := store.Set(ctx, key, cache.Entry{Value: []byte("secret")}, cache.Policy{TTL: time.Minute}); err != nil {
		t.Fatal(err)
	}
	if _, hit, err := store.Get(ctx, key); err != nil || !hit {
		t.Fatalf("hit=%v err=%v", hit, err)
	}
	if err := store.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	got := collector.Snapshot()
	if len(got) != 4 {
		t.Fatalf("events=%v", got)
	}
	for _, metric := range got {
		if metric.Route != "cache" {
			t.Fatalf("unexpected route: %+v", metric)
		}
	}
}

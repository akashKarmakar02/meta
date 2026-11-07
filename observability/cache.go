package observability

import (
	"context"
	"time"

	"github.com/akashKarmakar02/meta/cache"
)

// WrapCache observes store operations without recording keys or values. Use
// the returned store consistently when constructing cache invalidators.
func WrapCache(store cache.Store) cache.Store { return &observedCache{store: store} }

type observedCache struct{ store cache.Store }

func (s *observedCache) Get(ctx context.Context, key cache.Key) (cache.Entry, bool, error) {
	start := time.Now()
	entry, ok, err := s.store.Get(ctx, key)
	outcome := "miss"
	if err != nil {
		outcome = "error"
	} else if ok {
		outcome = "hit"
	}
	Record(ctx, "cache", "cache", "get", outcome, time.Since(start))
	return entry, ok, err
}
func (s *observedCache) Set(ctx context.Context, key cache.Key, entry cache.Entry, policy cache.Policy) error {
	start := time.Now()
	err := s.store.Set(ctx, key, entry, policy)
	outcome := "ok"
	if err != nil {
		outcome = "error"
	}
	Record(ctx, "cache", "cache", "set", outcome, time.Since(start))
	return err
}
func (s *observedCache) Delete(ctx context.Context, key cache.Key) error {
	start := time.Now()
	err := s.store.Delete(ctx, key)
	outcome := "ok"
	if err != nil {
		outcome = "error"
	}
	Record(ctx, "cache", "cache", "delete", outcome, time.Since(start))
	return err
}

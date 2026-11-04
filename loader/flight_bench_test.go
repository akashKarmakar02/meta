package loader

import (
	"context"
	"strings"
	"testing"
	"time"

	"go-meta/cache"
	"go-meta/routecontext"
)

func BenchmarkFlightMiss(b *testing.B) {
	for _, size := range []int{32, 4096} {
		b.Run(fmtSize(size), func(b *testing.B) {
			data := testData{Name: strings.Repeat("x", size), Values: []string{"one", "two"}}
			load := func(context.Context, routecontext.Route) (testData, error) { return data, nil }
			store := memory(b)
			inv, err := NewInvalidator(store)
			if err != nil {
				b.Fatal(err)
			}
			group, err := NewFlightGroup(inv, time.Second)
			if err != nil {
				b.Fatal(err)
			}
			h := registered(b, "r", load, CacheOptions[testData]{Store: store, Policy: cache.Policy{TTL: time.Hour}, Scope: publicScope, Codec: JSONCodec[testData](), Invalidator: inv, Flights: group})
			r := route("r", "1", nil)
			key, _ := LoaderCacheKey(r, "public-v1")
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if err := inv.InvalidateKey(ctx, key); err != nil {
					b.Fatal(err)
				}
				if _, err := h.Execute(ctx, r); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkFlightConcurrentWaiters measures a synchronized burst of eight
// waiters including coordination, codec fan-out, and one cache write.
func BenchmarkFlightConcurrentWaiters(b *testing.B) {
	var release chan struct{}
	started := make(chan struct{}, 1)
	load := func(ctx context.Context, _ routecontext.Route) (flightData, error) {
		started <- struct{}{}
		select {
		case <-release:
			return flightData{Items: []string{"value"}}, nil
		case <-ctx.Done():
			return flightData{}, ctx.Err()
		}
	}
	group, inv, _, h, r := newFlightTest(b, load)
	key, err := LoaderCacheKey(r, "public-v1")
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := inv.InvalidateKey(ctx, key); err != nil {
			b.Fatal(err)
		}
		release = make(chan struct{})
		results := make([]<-chan result, 8)
		results[0] = startFlight(ctx, h, r)
		<-started
		for i := 1; i < len(results); i++ {
			results[i] = startFlight(ctx, h, r)
		}
		waitFlights(b, group, 1, 8)
		close(release)
		for _, ch := range results {
			if got := receive(b, ch); got.err != nil {
				b.Fatal(got.err)
			}
		}
	}
}

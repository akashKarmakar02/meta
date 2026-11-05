package loader

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go-meta/cache"
	"go-meta/routecontext"
)

// E06 §30–§33: realistic workload benchmarks comparing execution modes.
// Workloads:
//
//	A trivial CPU-only        — value already available
//	B moderate CPU-bound     — deterministic hashing loop
//	C database-like          — 2ms controlled delay, cancellation-aware
//	D network-like           — 8ms external-call analogue
//	E medium serialization   — ~4 KiB structured data
//	F larger serialization   — ~48 KiB structured data
//
// Modes: uncached, cached miss, cached hit, flights miss, flights burst.

type benchRow struct {
	N      int
	Value  string
	Values []string
}

func benchRows(n int) []benchRow {
	rows := make([]benchRow, n)
	for i := range rows {
		rows[i] = benchRow{
			Value:  "row-" + strconv.Itoa(i),
			Values: []string{"alpha", "beta", "gamma", "delta"},
		}
	}
	return rows
}

func benchLoaders(b *testing.B) map[string]func(context.Context, routecontext.Route) (benchRow, error) {
	return map[string]func(context.Context, routecontext.Route) (benchRow, error){
		"A-trivial": func(context.Context, routecontext.Route) (benchRow, error) {
			return benchRow{Value: "constant"}, nil
		},
		"B-cpu": func(_ context.Context, r routecontext.Route) (benchRow, error) {
			id, _ := r.Params.Get("id")
			sum := sha256.Sum256([]byte(id))
			for round := 0; round < 200; round++ {
				sum = sha256.Sum256(sum[:])
			}
			return benchRow{Value: hex.EncodeToString(sum[:8])}, nil
		},
		"C-dblike": func(ctx context.Context, _ routecontext.Route) (benchRow, error) {
			select {
			case <-time.After(2 * time.Millisecond):
			case <-ctx.Done():
				return benchRow{}, ctx.Err()
			}
			return benchRow{Value: "db-row"}, nil
		},
		"D-netlike": func(ctx context.Context, _ routecontext.Route) (benchRow, error) {
			select {
			case <-time.After(8 * time.Millisecond):
			case <-ctx.Done():
				return benchRow{}, ctx.Err()
			}
			return benchRow{Value: "net-row"}, nil
		},
		"E-4kib": func(context.Context, routecontext.Route) (benchRow, error) {
			return benchRow{Value: string(make([]byte, 0)), Values: valuesOf(4 * 1024)}, nil
		},
		"F-48kib": func(context.Context, routecontext.Route) (benchRow, error) {
			return benchRow{Values: valuesOf(48 * 1024)}, nil
		},
	}
}

func valuesOf(n int) []string {
	unit := 32 // bytes per string approx
	out := make([]string, n/unit)
	for i := range out {
		out[i] = "0123456789012345678901234567890a"[:len(out[i])]
		out[i] = fmt.Sprintf("%031d", i)
	}
	return out
}

func benchStore(b *testing.B) cache.Store {
	m, err := cache.NewMemory(cache.MemoryConfig{MaxEntries: 1024, MaxBytes: 8 << 20, MaxEntryBytes: 512 << 10})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { m.Close() })
	return m
}

// BenchmarkE06WorkloadModes compares modes per workload for a single request.
func BenchmarkE06WorkloadModes(b *testing.B) {
	for name, load := range benchLoaders(b) {
		store := benchStore(b)
		r := route("bench", "42", nil)
		ctx := context.Background()

		baseOpts := CacheOptions[benchRow]{
			Store: store, Policy: cache.Policy{TTL: time.Hour},
			Scope: publicScope, Codec: JSONCodec[benchRow](),
		}
		b.Run(name+"/uncached", func(b *testing.B) {
			reg, err := NewRegistry([]string{"bench"}, Bind("bench", load))
			if err != nil {
				b.Fatal(err)
			}
			h, _ := reg.Lookup("bench")
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := h.Execute(ctx, r); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(name+"/cached-miss", func(b *testing.B) {
			h := registered(b, "bench", load, baseOpts)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				r2 := route("bench", strconv.Itoa(i), nil) // fresh key each op
				if _, err := h.Execute(ctx, r2); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(name+"/cached-hit", func(b *testing.B) {
			h := registered(b, "bench", load, baseOpts)
			if _, err := h.Execute(ctx, r); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := h.Execute(ctx, r); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkE06FlightBursts measures coalescing across synchronized bursts
// of concurrent waiters, with and without singleflight, for a database-like
// loader. Verifies the loader executes once per burst with flights enabled.
func BenchmarkE06FlightBursts(b *testing.B) {
	for _, waiters := range []int{1, 2, 4, 8, 16} {
		for _, useFlight := range []bool{false, true} {
			label := fmt.Sprintf("%d-waiters/flights=%v", waiters, useFlight)
			b.Run(label, func(b *testing.B) {
				store := benchStore(b)
				inv, err := NewInvalidator(store)
				if err != nil {
					b.Fatal(err)
				}
				opts := CacheOptions[benchRow]{
					Store: store, Policy: cache.Policy{TTL: time.Hour},
					Scope: publicScope, Codec: JSONCodec[benchRow](), Invalidator: inv,
				}
				var calls atomic.Int64
				load := func(ctx context.Context, _ routecontext.Route) (benchRow, error) {
					calls.Add(1)
					select {
					case <-time.After(2 * time.Millisecond):
					case <-ctx.Done():
						return benchRow{}, ctx.Err()
					}
					return benchRow{Value: "db"}, nil
				}
				if useFlight {
					group, err := NewFlightGroup(inv, 5*time.Second)
					if err != nil {
						b.Fatal(err)
					}
					opts.Flights = group
				}
				h := registered(b, "bench", load, opts)
				ctx := context.Background()
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					key := strconv.Itoa(i)
					start := make(chan struct{})
					var wg sync.WaitGroup
					for w := 0; w < waiters; w++ {
						wg.Go(func() {
							<-start
							r := route("bench", key, nil)
							if _, err := h.Execute(ctx, r); err != nil {
								b.Error(err)
							}
						})
					}
					close(start)
					wg.Wait()
				}
			})
		}
	}
}

// BenchmarkE06HitPathComponents profiles the cached-hit path by isolating its
// parts: key construction, scope callback and store Get alone.
func BenchmarkE06HitPathComponents(b *testing.B) {
	r := route("bench", "42", nil)
	ctx := context.Background()
	b.Run("key-construction", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := LoaderCacheKey(r, "public-v1"); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("scope-callback", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, _, err := publicScope(ctx, r); err != nil {
				b.Fatal(err)
			}
		}
	})
	store := benchStore(b)
	key, err := LoaderCacheKey(r, "public-v1")
	if err != nil {
		b.Fatal(err)
	}
	policy := cache.Policy{TTL: time.Hour}
	if err := store.Set(ctx, key, cache.Entry{Value: []byte(`{"Value":"x"}`)}, policy); err != nil {
		b.Fatal(err)
	}
	b.Run("store-get-hit", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, hit, err := store.Get(ctx, key); err != nil || !hit {
				b.Fatal(hit, err)
			}
		}
	})
	codec := JSONCodec[benchRow]()
	raw := []byte(`{"Value":"x"}`)
	b.Run("typed-decode", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := codec.Decode(raw); err != nil {
				b.Fatal(err)
			}
		}
	})
}

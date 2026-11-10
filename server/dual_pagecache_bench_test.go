package server

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/akashKarmakar02/meta/cache"
	"github.com/akashKarmakar02/meta/observability"
	"github.com/akashKarmakar02/meta/pagecache"
)

// These benchmarks include Fiber dispatch and response serialization, using the
// same typed loader, templ page/layout and metadata as the regression tests.
// Invalidation/setup is excluded from generation timing. The loader is in-memory;
// no synthetic database delay is added to bias the comparison toward caching.
func BenchmarkSSRRepresentations(b *testing.B) {
	for _, scenario := range []string{"uncached-html", "uncached-navigation", "dual-generation", "cached-html", "cached-navigation", "concurrent-mixed-generation"} {
		b.Run(scenario, func(b *testing.B) {
			f := newDualFixture()
			store, err := cache.NewMemory(cache.MemoryConfig{MaxEntries: 100, MaxBytes: 1 << 20})
			if err != nil {
				b.Fatal(err)
			}
			pc := pagecache.NewPageCache(store, cache.Never, pagecache.PublicRoutes(dualRouteID))
			cfg := f.config(pc)
			cfg.Observability = &observability.Config{DisableLogging: true}
			if scenario == "uncached-html" || scenario == "uncached-navigation" {
				cfg.PageCache = nil
			}
			app, err := New(cfg)
			if err != nil {
				b.Fatal(err)
			}
			defer app.Shutdown()
			if scenario == "cached-html" || scenario == "cached-navigation" {
				if _, _, err := dualRequest(app, false, dualPath, nil); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			for b.Loop() {
				if scenario == "dual-generation" || scenario == "concurrent-mixed-generation" {
					b.StopTimer()
					if err := pc.InvalidatePath(context.Background(), "/blog/example"); err != nil {
						b.Fatal(err)
					}
					b.StartTimer()
				}
				if scenario == "concurrent-mixed-generation" {
					var wg sync.WaitGroup
					errs := make(chan error, 8)
					for i := range 8 {
						wg.Add(1)
						go func() {
							defer wg.Done()
							res, _, err := dualRequest(app, i%2 == 1, dualPath, nil)
							if err == nil && res.StatusCode != 200 {
								err = fmt.Errorf("status %d", res.StatusCode)
							}
							errs <- err
						}()
					}
					wg.Wait()
					close(errs)
					for err := range errs {
						if err != nil {
							b.Fatal(err)
						}
					}
				} else {
					nav := scenario == "uncached-navigation" || scenario == "cached-navigation"
					res, _, err := dualRequest(app, nav, dualPath, nil)
					if err != nil {
						b.Fatal(err)
					}
					if res.StatusCode != 200 {
						b.Fatal(res.StatusCode)
					}
				}
			}
		})
	}
}

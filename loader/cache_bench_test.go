package loader

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/akashKarmakar02/meta/cache"
	"github.com/akashKarmakar02/meta/routecontext"
)

func BenchmarkCachedLoader(b *testing.B) {
	for _, size := range []int{32, 4096} {
		b.Run(fmtSize(size), func(b *testing.B) {
			data := testData{Name: strings.Repeat("x", size), Values: []string{"one", "two"}}
			load := func(context.Context, routecontext.Route) (testData, error) { return data, nil }
			r := route("r", "1", nil)
			ctx := context.Background()
			store := memory(b)
			h := registered(b, "r", load, CacheOptions[testData]{Store: store, Policy: cache.Policy{TTL: time.Hour}, Scope: publicScope, Codec: JSONCodec[testData]()})
			b.Run("uncached", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					_, _ = Execute(ctx, r, load)
				}
			})
			b.Run("hit", func(b *testing.B) {
				_, _ = h.Execute(ctx, r)
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					_, _ = h.Execute(ctx, r)
				}
			})
			b.Run("miss", func(b *testing.B) {
				key, _ := LoaderCacheKey(r, "public-v1")
				b.ReportAllocs()
				for b.Loop() {
					_ = store.Delete(ctx, key)
					_, _ = h.Execute(ctx, r)
				}
			})
			codec := JSONCodec[testData]()
			b.Run("codec", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					raw, _ := codec.Encode(data)
					_, _ = codec.Decode(raw)
				}
			})
		})
	}
}
func fmtSize(size int) string {
	if size == 32 {
		return "small"
	}
	return "medium"
}

// Binary fixture for real-browser tests of the public server integration.
// It uses the production Go server and compiled templ/Svelte assets, with no
// auth/CSRF middleware because the opted-in pages are public and have no forms.
package main

import (
	"log"
	"os"
	"strconv"
	"sync/atomic"

	"github.com/a-h/templ"
	"github.com/akashKarmakar02/meta/cache"
	generated "github.com/akashKarmakar02/meta/generated"
	"github.com/akashKarmakar02/meta/pagecache"
	"github.com/akashKarmakar02/meta/routing"
	"github.com/akashKarmakar02/meta/server"
	"github.com/gofiber/fiber/v3"
)

func main() {
	store, err := cache.NewMemory(cache.MemoryConfig{MaxEntries: 100, MaxBytes: 4 << 20})
	if err != nil {
		log.Fatal(err)
	}
	var generations atomic.Int64
	app, err := server.New(server.Config{
		Routes: generated.RegisterRoutes(), SSRPage: generated.SSRPage,
		BarePage:     func(r routing.Route) (templ.Component, error) { generations.Add(1); return generated.BarePage(r) },
		VisualLayout: generated.VisualLayout, ClientDir: "dist/client", GlobalStylesheet: true,
		PageCache: pagecache.NewPageCache(store, cache.Never, pagecache.PublicRoutes("page.templ", "about/page.templ")),
		Middleware: []fiber.Handler{func(c fiber.Ctx) error {
			err := c.Next()
			c.Set("X-Test-Generations", strconv.FormatInt(generations.Load(), 10))
			return err
		}},
	})
	if err != nil {
		log.Fatal(err)
	}
	log.Fatal(app.Listen(":" + os.Getenv("PORT")))
}

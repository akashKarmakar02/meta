package render

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/a-h/templ"
	"github.com/gofiber/fiber/v3"

	"github.com/akashKarmakar02/meta/internal/httpserver"
	"github.com/akashKarmakar02/meta/internal/metastate"
	"github.com/akashKarmakar02/meta/internal/routing"
	"github.com/akashKarmakar02/meta/internal/ssrresponse"
)

// SSRPairGenerator deliberately builds both representations, without invoking
// a response handler twice. The page and visual layout each render once.
// The page renders at the layout's child invocation, preserving layout-provided
// context. Capture it there rather than parsing the document or replaying the
// tree. PageContext preserves overrides when a layout sets defaults afterwards.
func SSRPairGenerator(route routing.Route, assets *httpserver.Assets, bare PageResolver, resolve VisualLayoutResolver) func(fiber.Ctx) (*ssrresponse.Page, error) {
	tags := entryTags(assets)
	return func(c fiber.Ctx) (*ssrresponse.Page, error) {
		renderCtx := metastate.New(c.Context())
		component, err := bare(route)
		if err != nil {
			return nil, fmt.Errorf("routes: BarePage: %w", err)
		}
		var pageHTML []byte
		var pageErr error
		rendered := false
		capture := templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
			// Memoize even if a custom layout invokes its child more than once.
			if !rendered {
				rendered = true
				pageHTML, pageErr = RenderComponent(metastate.PageContext(ctx), component)
			}
			if pageErr != nil {
				return pageErr
			}
			_, err := w.Write(pageHTML)
			return err
		})
		visual, err := resolve(route, capture)
		if err != nil {
			return nil, fmt.Errorf("routes: VisualLayout: %w", err)
		}
		visualHTML, err := RenderComponent(renderCtx, visual)
		if err != nil {
			return nil, fmt.Errorf("render visual layout %s: %w", route.Path, err)
		}
		if !rendered {
			return nil, fmt.Errorf("render visual layout %s: page outlet was not rendered", route.Path)
		}
		meta := metastate.Get(renderCtx)
		env := NavigationEnvelope(c, route, string(pageHTML))
		env.HTML = string(visualHTML)
		if meta.Title != "" {
			env.Title = meta.Title
		}
		if meta != (metastate.Page{}) {
			env.Metadata = &meta
		}
		navigation, err := httpserver.MarshalNavigation(env)
		if err != nil {
			return nil, fmt.Errorf("marshal navigation response: %w", err)
		}
		title := "go-meta"
		if meta.Title != "" {
			title = meta.Title
		}
		status := c.Response().StatusCode()
		// Fiber header strings are borrowed until handler return. Flights may
		// outlive their owner, so representation headers must own their bytes.
		control := strings.Clone(c.GetRespHeader(fiber.HeaderCacheControl))
		language := strings.Clone(c.GetRespHeader(fiber.HeaderContentLanguage))
		return &ssrresponse.Page{
			HTML:       ssrresponse.Response{StatusCode: status, ContentType: "text/html; charset=utf-8", CacheControl: control, ContentLanguage: language, Body: []byte(documentWithMetadata(string(visualHTML), tags, title, "", meta))},
			Navigation: ssrresponse.Response{StatusCode: status, ContentType: "application/json", CacheControl: "no-store", ContentLanguage: language, Body: navigation},
		}, nil
	}
}

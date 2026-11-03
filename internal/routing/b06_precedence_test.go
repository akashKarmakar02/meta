package routing

import (
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gofiber/fiber/v3"
)

func TestB06FiberRegistrationPrecedence(t *testing.T) {
	cases := []struct{ name, a, b, url, winner string }{
		{"static root", "about/page.templ", "[page]/page.svelte", "/about", "about/page.templ"},
		{"static sibling", "products/new/page.templ", "products/[id]/page.svelte", "/products/new", "products/new/page.templ"},
		{"dynamic catchall", "docs/[slug]/page.svelte", "docs/[...path]/page.templ", "/docs/install", "docs/[slug]/page.svelte"},
		{"static catchall", "docs/install/page.svelte", "docs/[...path]/page.templ", "/docs/install", "docs/install/page.svelte"},
		{"nested static different names", "users/[z]/settings/page.templ", "users/[a]/[section]/page.svelte", "/users/1/settings", "users/[z]/settings/page.templ"},
		{"leftmost static", "a/[id]/details/page.templ", "a/static/[section]/page.svelte", "/a/static/details", "a/static/[section]/page.svelte"},
		{"static prefix catchall", "a/static/[...rest]/page.templ", "a/[id]/[name]/page.svelte", "/a/static/value", "a/static/[...rest]/page.templ"},
		{"unicode static", "a/é/page.templ", "a/[id]/page.svelte", "/a/%C3%A9", "a/é/page.templ"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			routes, err := Discover(fstest.MapFS{tc.a: {}, tc.b: {}})
			if err != nil {
				t.Fatal(err)
			}
			app := fiber.New(fiber.Config{CaseSensitive: true})
			for _, r := range routes {
				r := r
				app.Get(FiberRegistrationPath(r.Path), func(c fiber.Ctx) error { return c.SendString(r.SourceFile) })
			}
			resp, err := app.Test(httptest.NewRequest("GET", tc.url, nil))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			b, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimPrefix(string(b), "app/"); got != tc.winner {
				t.Fatalf("winner = %q, want %q; registration: %+v", got, tc.winner, routes)
			}
		})
	}
}

func TestB06ConflictsAndDeterminism(t *testing.T) {
	for _, tc := range []struct{ a, b string }{
		{"users/[id]/page.templ", "users/[name]/page.svelte"},
		{"docs/[...slug]/page.templ", "docs/[...path]/page.svelte"},
		{"docs/[...slug]/page.templ", "docs/[id]/[post]/page.svelte"},
	} {
		t.Run(tc.a, func(t *testing.T) {
			_, err := Discover(fstest.MapFS{tc.a: {}, tc.b: {}})
			if err == nil || !strings.Contains(err.Error(), tc.a) || !strings.Contains(err.Error(), tc.b) {
				t.Fatalf("expected source-aware conflict, got %v", err)
			}
		})
	}
	fsys := fstest.MapFS{"a/[z]/static/page.templ": {}, "a/[a]/[id]/page.svelte": {}, "a/fixed/[...tail]/page.templ": {}, "a/fixed/[id]/page.svelte": {}}
	first, err := Discover(fsys)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		got, err := Discover(fsys)
		if err != nil {
			t.Fatal(err)
		}
		for j := range got {
			if got[j].ID != first[j].ID {
				t.Fatalf("unstable order: %v %v", first, got)
			}
		}
	}
}

func TestB06CatchAllParameterLimit(t *testing.T) {
	var dirs []string
	for i := 0; i < 29; i++ {
		dirs = append(dirs, fmt.Sprintf("[p%d]", i))
	}
	dirs = append(dirs, "[...rest]", "page.svelte")
	if _, err := Discover(fstest.MapFS{strings.Join(dirs, "/"): {}}); err != nil {
		t.Fatalf("29 named parameters plus catch-all should be accepted: %v", err)
	}
}

func TestB06EncodedLiteralOverlapRejected(t *testing.T) {
	for _, tc := range []struct{ a, b string }{
		{"a/!/page.templ", "a/[id]/page.svelte"},
		{"a/normal/!/page.templ", "a/[...tail]/page.svelte"},
	} {
		_, err := Discover(fstest.MapFS{tc.a: {}, tc.b: {}})
		if err == nil || !strings.Contains(err.Error(), tc.a) || !strings.Contains(err.Error(), tc.b) {
			t.Fatalf("expected source-aware unsafe literal overlap error, got %v", err)
		}
	}
}

func TestB06FiberMetacharactersRejected(t *testing.T) {
	for _, segment := range []string{"a:b", "a+b", "a*b", "a?b", `a\b`} {
		_, err := Discover(fstest.MapFS{"x/" + segment + "/page.svelte": {}})
		if err == nil || !strings.Contains(err.Error(), "INVALID_SEGMENT") {
			t.Fatalf("%q: expected invalid segment error, got %v", segment, err)
		}
	}
}

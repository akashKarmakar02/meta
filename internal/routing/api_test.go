package routing

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestDiscoverAPI(t *testing.T) {
	fsys := fstest.MapFS{
		"api/posts/route.go":      {Data: []byte("package posts\nfunc GET(c fiber.Ctx) error { return nil }\nfunc POST(c fiber.Ctx) error { return nil }")},
		"api/posts/[id]/route.go": {Data: []byte("//go:build ignore\n\npackage post\nfunc DELETE(c fiber.Ctx) error { return nil }")},
	}
	got, err := DiscoverAPI(fsys, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Route.Path != "/api/posts/:id" || got[1].Route.Path != "/api/posts" {
		t.Fatalf("routes: %#v", got)
	}
	if strings.Join(got[1].Methods, ",") != "GET,POST" {
		t.Fatalf("methods: %v", got[1].Methods)
	}
}

func TestDiscoverAPIRejectsPageConflict(t *testing.T) {
	fsys := fstest.MapFS{"api/route.go": {Data: []byte("package api\nfunc GET(c fiber.Ctx) error { return nil }")}}
	page, err := NewRoute("/api", "api/page.templ", ModeSSR, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DiscoverAPI(fsys, []Route{page}); err == nil {
		t.Fatal("expected a conflict")
	}
}

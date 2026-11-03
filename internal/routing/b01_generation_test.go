package routing

import (
	"io/fs"
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

// B01 generation-ordering tests: malformed routes must be rejected before
// any generated output changes. routegen's flow is Discover -> VerifySymbols
// -> WriteRoutesFile; these tests pin the Discover stage of that contract.

func TestB01MalformedRouteRejectedBeforeGeneration(t *testing.T) {
	cases := []struct {
		name    string
		dir     string
		wantErr string
	}{
		{"unbalanced open", "blog/[[id", "brackets"},
		{"unbalanced close", "blog/id]", "brackets"},
		{"catch-all unbalanced", "docs/[...slug", "brackets"},
		{"catch-all empty name", "docs/[...]", "empty"},
		{"empty name", "blog/[]", "empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fsys := fstest.MapFS{
				"index.templ":           &fstest.MapFile{Data: []byte("x")},
				"dashboard/page.svelte": &fstest.MapFile{Data: []byte("x")},
				tc.dir + "/page.svelte": &fstest.MapFile{Data: []byte("x")},
			}
			routes, err := Discover(fsys)
			if err == nil {
				t.Fatalf("route %q was not rejected (routes: %+v)", tc.dir, routes)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not mention %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestB01ValidDynamicRoutesPreserved(t *testing.T) {
	fsys := fstest.MapFS{
		"index.templ":                       &fstest.MapFile{Data: []byte("x")},
		"products/[id]/page.svelte":         &fstest.MapFile{Data: []byte("x")},
		"products/new/page.templ":           &fstest.MapFile{Data: []byte("x")},
		"users/[user_id]/posts/page.svelte": &fstest.MapFile{Data: []byte("x")},
	}
	routes, err := Discover(fsys)
	if err != nil {
		t.Fatal(err)
	}
	wantPaths := map[string]bool{
		"/products/:id":         true,
		"/products/new":         true,
		"/users/:user_id/posts": true,
	}
	for _, r := range routes {
		delete(wantPaths, r.Path)
	}
	if len(wantPaths) > 0 {
		for p := range wantPaths {
			t.Errorf("expected route %s missing from discovery", p)
		}
	}
}

// Deterministic rejection: two structurally equivalent routes with different
// param names at the same position must fail identically regardless of walk
// order, and the error must name both sources so the developer can resolve.
func TestB01EquivalentRoutesRejectedDeterministically(t *testing.T) {
	buildFS := func(a, b string) fstest.MapFS {
		return fstest.MapFS{
			"index.templ":      &fstest.MapFile{Data: []byte("x")},
			a + "/page.svelte": &fstest.MapFile{Data: []byte("x")},
			b + "/page.svelte": &fstest.MapFile{Data: []byte("x")},
		}
	}
	for _, pair := range [][2]string{{"[id]", "[slug]"}, {"[slug]", "[id]"}} {
		_, err := Discover(buildFS(pair[0], pair[1]))
		if err == nil {
			t.Fatalf("equivalent routes %v not rejected", pair)
		}
		if !strings.Contains(err.Error(), pair[0]+"/page.svelte") || !strings.Contains(err.Error(), pair[1]+"/page.svelte") {
			t.Fatalf("error %q does not name both sources", err.Error())
		}
	}
}

// The repo's own app tree must stay discoverable: guards against a rule
// change that would silently break the working site.
func TestB01RepoTreeStillDiscovers(t *testing.T) {
	sub, err := fs.Sub(os.DirFS("../.."), "app")
	if err != nil {
		t.Fatal(err)
	}
	routes, err := Discover(sub)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, r := range routes {
		found[r.Path] = true
	}
	for _, want := range []string{"/", "/dashboard", "/dashboard/settings", "/products/:id"} {
		if !found[want] {
			t.Errorf("repo app tree missing expected route %s (found %v)", want, found)
		}
	}
}

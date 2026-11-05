package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A separate Go module runs the actual generator command, compiles its
// output, and renders static and dynamic templ pages with their layouts.
func TestIndependentProjectGeneration(t *testing.T) {
	framework, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	goSum, err := os.ReadFile(filepath.Join(framework, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"go.sum":                       string(goSum),
		"go.mod":                       "module example.com/myapp\n\ngo 1.26.10\n\nrequire (\n github.com/akashKarmakar02/meta v0.0.0\n github.com/a-h/templ v0.3.1100\n)\n\nreplace github.com/akashKarmakar02/meta => " + strconv.Quote(framework) + "\n\ntool github.com/a-h/templ/cmd/templ\n",
		"client/.vite/manifest.json":   `{"frontend/entry.ts":{"file":"assets/entry-a.js","isEntry":true}}`,
		"client/assets/entry-a.js":     "// client",
		"app/dashboard/page.svelte":    "<h1>Dashboard</h1>",
		"app/page.templ":               "package app\n\ntempl Page() { <main>Home</main> }\n",
		"app/layout.templ":             "package app\n\ntempl Layout(c templ.Component) { <section>@c</section> }\n",
		"app/about/page.templ":         "package about\n\ntempl About() { <main>About</main> }\n",
		"app/blog/[slug]/page.templ":   "package post\n\ntempl Page() { <main>Post</main> }\n",
		"app/blog/[slug]/layout.templ": "package post\n\ntempl Layout(c templ.Component) { <article>@c</article> }\n",
		"contract_test.go": `package consumer
import (
 "bytes"
 "context"
 "testing"
 generated "example.com/myapp/generated"
 "github.com/akashKarmakar02/meta/routing"
)
func TestGeneratedPages(t *testing.T) {
 want := map[string]string{"/":"<section><main>Home</main></section>", "/about":"<section><main>About</main></section>", "/blog/:slug":"<article><main>Post</main></article>"}
 for _, route := range generated.RegisterRoutes() {
  if route.Mode == routing.ModeCSR { continue }
  var public routing.Route = route
  component, err := generated.SSRPage(public); if err != nil { t.Fatal(err) }
  var out bytes.Buffer
  if err := component.Render(context.Background(), &out); err != nil { t.Fatal(err) }
  if out.String() != want[route.Path] { t.Fatalf("%s: %q", route.Path, out.String()) }
  delete(want, route.Path)
 }
 if len(want) != 0 { t.Fatalf("missing routes: %v", want) }
}
`,
		"runtime_test.go": `package consumer
import (
 "context"
 "io"
 "net"
 "net/http"
 "net/http/httptest"
 "strings"
 "sync/atomic"
 "testing"
 generated "example.com/myapp/generated"
 "github.com/akashKarmakar02/meta/action"
 "github.com/akashKarmakar02/meta/loader"
 "github.com/akashKarmakar02/meta/routecontext"
 "github.com/akashKarmakar02/meta/server"
)
func TestIndependentRuntime(t *testing.T) {
 var loads, mutations atomic.Int32
 app, err := server.New(server.Config{
  Routes: generated.RegisterRoutes(), SSRPage: generated.SSRPage, BarePage: generated.BarePage, VisualLayout: generated.VisualLayout,
  ClientDir: "client", TrustedOrigin: "https://example.com",
  Loaders: []loader.Binding{loader.Bind("about/page.templ", func(context.Context, routecontext.Route) (string,error) { loads.Add(1); return "value", nil })},
  Actions: []action.Binding{action.Bind("save", func(_ context.Context, _ routecontext.Route, v struct { Name string }) (string,error) { mutations.Add(1); return v.Name,nil })},
 })
 if err != nil { t.Fatal(err) }
 for path, want := range map[string]string{"/about":"<section><main>About</main></section>", "/dashboard":"data-go-route-id=\"dashboard/page.svelte\"", "/assets/entry-a.js":"// client"} {
  res, err := app.Test(httptest.NewRequest("GET", path, nil)); if err != nil { t.Fatal(err) }; body,err:=io.ReadAll(res.Body); res.Body.Close(); if err != nil || res.StatusCode != 200 || !strings.Contains(string(body), want) { t.Fatalf("%s %d %s %v", path,res.StatusCode,body,err) }
 }
 if loads.Load() != 1 { t.Fatalf("loader calls: %d",loads.Load()) }
 req:=httptest.NewRequest("POST", "/_meta/actions/save", strings.NewReader("{\"name\":\"saved\"}")); req.Header.Set("Origin","https://example.com"); req.Header.Set("Content-Type","application/json")
 res,err:=app.Test(req); if err!=nil { t.Fatal(err) }; body,err:=io.ReadAll(res.Body); res.Body.Close(); if err!=nil || res.StatusCode!=200 || !strings.Contains(string(body),"saved") || mutations.Load()!=1 { t.Fatalf("action %d %s %v",res.StatusCode,body,err) }
 ln,err:=net.Listen("tcp", "127.0.0.1:0"); if err!=nil { t.Fatal(err) }
 done:=make(chan error,1); go func(){ done<-app.Listener(ln) }()
 response,err:=http.Get("http://"+ln.Addr().String()+"/about"); if err!=nil { app.Shutdown(); <-done; t.Fatal(err) }; data,err:=io.ReadAll(response.Body); response.Body.Close()
 if err:=app.Shutdown(); err!=nil { t.Fatal(err) }; if err:=<-done; err!=nil { t.Fatal(err) }
 if err!=nil || response.StatusCode!=200 || !strings.Contains(string(data),"About") { t.Fatalf("listener %d %s %v",response.StatusCode,data,err) }
}
`,
	}
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	commands := [][]string{
		{"list", "-mod=mod", "github.com/a-h/templ/cmd/templ"},
		{"tool", "templ", "generate", "-ignore-pattern", `[/\\]app[/\\].*\[|[/\\]generated[/\\]routepages[/\\]`},
		{"run", "github.com/akashKarmakar02/meta/cmd/routegen"},
		{"test", "./..."},
	}
	for _, args := range commands {
		cmd := exec.Command("go", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go %v: %v\n%s", args, err, output)
		}
	}
	generated, err := os.ReadFile(filepath.Join(root, "generated", "routes_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"example.com/myapp/app", "example.com/myapp/app/about", "example.com/myapp/generated/routepages/", "github.com/akashKarmakar02/meta/routing"} {
		if !strings.Contains(string(generated), want) {
			t.Errorf("generated imports missing %q", want)
		}
	}
	if strings.Contains(string(generated), "github.com/akashKarmakar02/meta/app") || strings.Contains(string(generated), "github.com/akashKarmakar02/meta/internal/") {
		t.Fatal("generated routes refer to framework app or internals")
	}
}

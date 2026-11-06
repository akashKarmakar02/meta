# Load data for a page

A loader is a Go function that reads data for a route before rendering. Register it against the source path relative to `app/`, not the URL. For example, `app/about/page.templ` has the route ID `about/page.templ`.

Create `app/loaders/about.go`:

```go
package loaders

import (
    "context"

    "github.com/akashKarmakar02/meta/routecontext"
)

type AboutData struct {
    Greeting string `json:"greeting"`
}

func About(_ context.Context, _ routecontext.Route) (AboutData, error) {
    return AboutData{Greeting: "Hello from Go"}, nil
}
```

Add imports for your package and `github.com/akashKarmakar02/meta/loader` to `cmd/app/main.go`. Add this field to the existing `server.Config`:

```go
Loaders: []loader.Binding{
    loader.Bind("about/page.templ", loaders.About),
},
```

In `app/about/page.templ`, read the exact Go type returned by the loader. Change the module path to match your `go.mod`:

```templ
package about

import "example.com/my-app/app/loaders"
import "github.com/akashKarmakar02/meta/loaderdata"

templ About() {
    {{ data, ok := loaderdata.Get[loaders.AboutData](ctx) }}
    <main>
        <h1>About</h1>
        if ok {
            <p>{ data.Greeting }</p>
        }
    </main>
}
```

Meta runs the loader for a matching page request before rendering. Handle route parameters through `route.Params` in the loader. For a Svelte route, register its `page.svelte` route ID instead. Meta JSON-serializes the result and passes it to the page as a `data` prop, so only return data that the browser may see.

Loaders should read data, not perform mutations. Return the public `loader` package's error sentinels or `loader.Redirect` where appropriate. Do not assume client disconnects cancel a loader; pass the supplied context to operations that support cancellation. See the [loader reference](../../README.md#executing-csr-loaders-c04) for CSR props and error behavior.

# Create an API

Create a `route.go` file to define an HTTP endpoint. Its directory determines the URL, and exported functions named after HTTP methods handle requests.

```text
app/api/posts/route.go       → /api/posts
app/api/posts/[id]/route.go  → /api/posts/:id
```

```go
// app/api/posts/route.go
package posts

import "github.com/gofiber/fiber/v3"

func GET(c fiber.Ctx) error {
    return c.JSON(fiber.Map{"posts": []string{"First post"}})
}

func POST(c fiber.Ctx) error {
    return c.Status(fiber.StatusCreated).JSON(fiber.Map{"created": true})
}
```

Dynamic routes must start with `//go:build ignore` and a blank line, before the package declaration. Go cannot import directories containing `[id]`; this constraint keeps Go's package discovery from treating the source directory as a package, and Meta removes it from the generated copy. Dynamic routes can read parameters with `c.Params("id")`. The supported methods are `GET`, `POST`, `PUT`, `PATCH`, `DELETE`, `HEAD`, and `OPTIONS`. Define `HEAD` and `OPTIONS` explicitly if needed; an unimplemented method returns 405 with an `Allow` header. An API route and a page cannot share the same URL. Framework paths such as `/_meta`, `/assets`, and `/healthz` are reserved.

```go
//go:build ignore

package post

import "github.com/gofiber/fiber/v3"

func GET(c fiber.Ctx) error {
    return c.JSON(fiber.Map{"id": c.Params("id")})
}
```

Meta generates endpoint bindings during `meta dev` and `meta build`; do not edit generated files. `route.go` is compiled as an isolated generated package so dynamic directories work in Go. Put shared helpers in a normal, importable Go package, or define private helpers in `route.go` itself. Other build constraints and `go:embed` directives in `route.go` are not supported.

New projects mount the generated endpoints automatically. For an existing project, add this field once to its `server.Config`:

```go
APIRoutes: generated.RegisterAPIRoutes(),
```

The example application includes `/api/posts` and `/api/posts/42`. API handlers run on the server and can return any Fiber response. They do not automatically run page loaders or actions. Protect and validate each endpoint as appropriate: API routes do not inherit actions' origin checks, form CSRF protection, authentication, or authorization.

For a typed JSON mutation, you can also register an action. Meta exposes it as `POST /_meta/actions/<id>` and handles JSON decoding and response formatting.

## Define an action

Create `app/actions/greeting.go`:

```go
package actions

import (
    "context"
    "strings"

    "github.com/akashKarmakar02/meta/action"
    "github.com/akashKarmakar02/meta/routecontext"
)

type GreetingInput struct {
    Name string `json:"name"`
}

type GreetingOutput struct {
    Message string `json:"message"`
}

func Greet(_ context.Context, _ routecontext.Route, in GreetingInput) (GreetingOutput, error) {
    name := strings.TrimSpace(in.Name)
    if name == "" {
        return GreetingOutput{}, action.NewValidationError("Please check your input.", map[string][]string{
            "name": {"Name is required."},
        })
    }
    return GreetingOutput{Message: "Hello, " + name + "!"}, nil
}
```

In `cmd/app/main.go`, add imports for your package and the action package. Use your module path in place of `example.com/my-app`:

```go
import (
    "github.com/akashKarmakar02/meta/action"
    "example.com/my-app/app/actions"
)
```

Add the following fields to the existing `server.Config` literal. Keep its generated route and asset fields:

```go
TrustedOrigin: "http://localhost:3000",
Actions: []action.Binding{
    action.Bind("greeting.create", actions.Greet),
},
```

For production, configure `TrustedOrigin` with the exact browser-facing HTTPS origin, including a non-default port if there is one. The generated starter does not configure action bindings or a trusted origin for you. Restart `meta dev` after changing the server configuration.

## Call it

With `meta dev` running on port 3000:

```sh
curl -i http://localhost:3000/_meta/actions/greeting.create \
  -H 'Origin: http://localhost:3000' \
  -H 'Content-Type: application/json' \
  -d '{"name":"Ada"}'
```

A successful request returns JSON:

```json
{"ok":true,"data":{"message":"Hello, Ada!"}}
```

The endpoint accepts POST and checks the origin. JSON decoding rejects unknown fields and extra JSON values. A structured validation error returns HTTP 422 with an `error` message and field messages in `errors`. Generic invalid input returns 400, and unexpected server errors use a generic response. Action responses have `Cache-Control: no-store`.

Meta does not authenticate or authorize callers. Check the user's identity and permissions in your application before performing a protected mutation. Origin checking is not authentication. Validate input before irreversible work. An action is called once per accepted request, but a client can send a second request.

## Other HTTP methods

`server.New` also returns a Fiber application, so you can register custom handlers directly before calling `Listen`. Those handlers are separate from file-based routes and actions. Check Fiber's route ordering when adding them.

For read data that belongs to a page, consider a [loader](loaders.md). For ordinary HTML forms, see [forms and validation](forms.md).

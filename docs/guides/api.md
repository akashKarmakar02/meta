# Create an API

Meta does not turn files such as `app/api/route.go` into HTTP endpoints. For a typed JSON mutation, register an action. Meta exposes it as `POST /_meta/actions/<id>` and handles JSON decoding and response formatting.

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

`server.New` returns a Fiber application, so you can register a custom handler on that app before calling `Listen`. This is a lower-level Fiber endpoint, not a file-based Meta route or an action. The application must handle validation, authentication, authorization, origin and CSRF policy, body limits, and errors appropriate to that handler. Meta registers its own handlers first; avoid paths that overlap page routes, catch-all routes, or reserved prefixes such as `/_meta` and `/assets`. Check Fiber's route ordering when adding handlers.

For read data that belongs to a page, consider a [loader](loaders.md). For ordinary HTML forms, see [forms and validation](forms.md).

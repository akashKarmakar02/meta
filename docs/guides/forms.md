# Forms and validation

Use a form-capable action when an HTML form should work without JavaScript. Meta accepts URL-encoded POST submissions, verifies the trusted origin and CSRF token, decodes the input, and runs the action. A successful native submission receives a 303 redirect to the configured local path.

## Register a form action

Write a decoder for the action's input type. This example uses the `GreetingInput` and `Greet` action from [create an API](api.md):

```go
func DecodeGreeting(values url.Values) (actions.GreetingInput, error) {
    return actions.GreetingInput{Name: values.Get("name")}, nil
}
```

Import `net/url` and your `actions` package where you define the decoder. In `cmd/app/main.go`, construct a CSRF manager before creating the server:

```go
csrf, err := formcsrf.New([]byte(os.Getenv("FORM_CSRF_SECRET")), nil)
if err != nil {
    log.Fatal(err)
}
```

Import `github.com/akashKarmakar02/meta/formcsrf`, `log`, and `os`. Supply the manager and a trusted origin in `server.Config`, then register the form binding:

```go
TrustedOrigin: "http://localhost:3000",
CSRF: csrf,
Actions: []action.Binding{
    action.BindForm("greeting.create", actions.Greet, DecodeGreeting, "/thanks"),
},
```

Create an actual `/thanks` page for the success destination. Set `FORM_CSRF_SECRET` to a persistent, randomly generated secret of at least 32 bytes before starting the server. Keep the secret outside source control and share the same secret across instances and restarts. In production, set `TrustedOrigin` to the actual public HTTPS origin. The manager is required for form bindings, and Meta does not create it for the starter application.

For example, replace the root `app/page.templ` with this form:

```templ
package app

import "github.com/akashKarmakar02/meta/formcsrf"

templ Page() {
    <main>
        <h1>Greeting</h1>
        <form method="post" action="/_meta/actions/greeting.create">
            @formcsrf.HiddenField()
            <label for="name">Name</label>
            <input id="name" name="name" required/>
            <button type="submit">Send</button>
        </form>
    </main>
}
```

Use the framework's hidden field helper instead of copying a submitted `_csrf` value. The signed cookie and token must match. Meta issues a host-only secure cookie for an HTTPS trusted origin. Token-bearing responses are not shared-cacheable.

## Validation feedback

An action or decoder can return `action.NewValidationError(message, fields)`. The API guide shows one example. Only put messages safe for the user in this error. A decoder error prevents the action from running. Structured errors return 422; malformed input and ordinary decoder errors return 400.

To redisplay an invalid form with field messages, attach `WithFormFeedback` to the binding. Its first argument is an explicit allowlist of non-sensitive values to preserve. Its renderer receives `action.FormState` and returns a complete `templ.Component` document:

```go
action.BindForm("greeting.create", actions.Greet, DecodeGreeting, "/thanks").
    WithFormFeedback([]string{"name"}, greetingform.Feedback)
```

Create `app/greetingform/feedback.templ` and import `example.com/my-app/app/greetingform` in the file that registers the binding:

```templ
package greetingform

import "github.com/akashKarmakar02/meta/action"
import "github.com/akashKarmakar02/meta/formcsrf"

templ Feedback(state action.FormState) {
    <!doctype html>
    <html lang="en">
        <head><meta charset="utf-8"/><title>Greeting</title></head>
        <body>
            <main>
                <h1>Greeting</h1>
                <form method="post" action="/_meta/actions/greeting.create">
                    @formcsrf.HiddenField()
                    if state.Message() != "" {
                        <p role="alert">{ state.Message() }</p>
                    }
                    <label for="name">Name</label>
                    if len(state.Errors("name")) > 0 {
                        <input id="name" name="name" value={ state.Value("name") } aria-invalid="true" aria-describedby="name-error"/>
                        <div id="name-error">
                            for _, message := range state.Errors("name") {
                                <p>{ message }</p>
                            }
                        </div>
                    } else {
                        <input id="name" name="name" value={ state.Value("name") }/>
                    }
                    <button type="submit">Send</button>
                </form>
            </main>
        </body>
    </html>
}
```

Templ escapes the values and messages. Do not allowlist passwords, authentication codes, tokens, payment details, or file values. Without a feedback renderer, Meta returns a generic 422 page.

The repository's [guestbook form and feedback renderer](../../app/guestbookform/form.templ) show a complete renderer and accessible field messages. The framework does not roll back side effects when validation returns an error, so validate before committing changes.

## Enhanced submissions

Add `data-go-meta-enhance` to an eligible same-origin action form to opt into Meta's browser enhancement. The normal form and CSRF requirements still apply. Without JavaScript, the browser uses the native submission and redirect. The [enhanced form reference](../../README.md#enhanced-native-forms-d05) describes supported forms and response handling.

## Cross-origin submissions

Native form validation is unaffected by CORS: enabling CORS for an origin never makes a cross-origin form submission valid, because it fails the trusted-origin check before the CSRF token is even consulted, and a same-origin submission still requires a matching signed token. The two mechanisms answer different questions — see the [CORS guide](cors.md) for the distinction between browser permission to read a response and server-side authorization of a mutation.

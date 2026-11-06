# Troubleshooting

## The shell cannot find `meta` or `air`

Check `go env GOBIN`. Add it to `PATH` if it is set. Otherwise add `$(go env GOPATH)/bin` to `PATH`. Restart the shell or run `hash -r` in Bash after changing `PATH`. See [installation](../getting-started/installation.md).

## `meta dev` reports missing Vite dependencies

Run `meta setup` in the project root. This resolves the templ tool and installs npm dependencies. Install Node.js/npm first if `npm` is missing.

## A new route is missing

Check that it is under `app/` and is named exactly `page.templ` or `page.svelte`. Check for conflicting page or layout files. Run `meta generate` and read its errors, or use `meta dev` to regenerate during development. See [pages and layouts](routing.md).

## The production server cannot find assets

Run `meta build` and start `./bin/app` from the project root. The default `ClientDir` is relative to the working directory. Deploy the matching `dist/client` directory with the binary.

## An action returns 403

Check that the request's `Origin` matches `TrustedOrigin` exactly, including scheme and port. For a native form, check the CSRF manager, hidden field, and browser cookie as well. A token can expire, so reload the page and submit again. Do not disable the origin or CSRF checks to work around deployment configuration.

## A form shows a generic validation page

Add a feedback renderer with `WithFormFeedback` and allowlist only fields safe to repopulate. See [forms and validation](forms.md).

## Changes to generated files disappear

Change the source page or component instead, then run `meta generate`. See [what not to edit](../getting-started/project-structure.md#what-not-to-edit).

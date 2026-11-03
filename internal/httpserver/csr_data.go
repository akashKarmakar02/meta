package httpserver

import "context"

// csrDataKey carries the JSON-encoded loader result for a CSR request. It is
// set by WithLoader (after running the registered loader) and read by the CSR
// render handler, which either embeds it in the boot shell or attaches it to
// the navigation envelope. The value is the output of encoding/json.Marshal,
// which HTML-escapes <, > and & — so the bytes are safe to embed verbatim in a
// <script type="application/json"> element without a separate escaping pass.
type csrDataKey struct{}

// WithCSRLoaderData attaches the JSON-encoded loader result to the request
// context for the remainder of the request. The bytes must be valid JSON
// (typically from encoding/json.Marshal).
func WithCSRLoaderData(ctx context.Context, data []byte) context.Context {
	return context.WithValue(ctx, csrDataKey{}, data)
}

// CSRLoaderData returns the JSON-encoded loader result attached by
// WithCSRLoaderData. ok is false when the request has no CSR loader result
// (no registered loader, or the route is not CSR). The returned bytes are the
// exact JSON to deliver to the browser; a loader that returned JSON null is
// represented as the 4-byte value "null", which is distinct from ok=false.
func CSRLoaderData(ctx context.Context) (data []byte, ok bool) {
	if ctx == nil {
		return nil, false
	}
	v, ok := ctx.Value(csrDataKey{}).([]byte)
	return v, ok
}

package render

import (
	"bytes"
	"context"

	"github.com/a-h/templ"
)

// RenderComponent renders a templ component to bytes through an in-memory
// buffer. The ctx flows into templ's rendering pipeline so cancellation and
// deadline propagation behave as the caller expects; SSR handlers pass the
// request context (D05 wires that handoff).
func RenderComponent(ctx context.Context, component templ.Component) ([]byte, error) {
	var b bytes.Buffer
	if err := component.Render(ctx, &b); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

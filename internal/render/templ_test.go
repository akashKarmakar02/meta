package render

import (
	"context"
	"errors"
	"io"
	"testing"
)

// stubComponent writes a fixed fragment, standing in for a templ-generated component.
type stubComponent struct{}

func (stubComponent) Render(ctx context.Context, w io.Writer) error {
	_, err := io.WriteString(w, `<p>fixture</p>`)
	return err
}

// canceledRender checks for cancellation mid-render, like templ components do.
type canceledRender struct{}

func (canceledRender) Render(ctx context.Context, w io.Writer) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestRenderComponentRendersTestComponent(t *testing.T) {
	got, err := RenderComponent(context.Background(), stubComponent{})
	if err != nil {
		t.Fatalf("RenderComponent() error = %v", err)
	}
	if want := `<p>fixture</p>`; string(got) != want {
		t.Errorf("RenderComponent() = %q, want %q", got, want)
	}
}

func TestRenderComponentPropagatesContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := RenderComponent(ctx, canceledRender{})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("RenderComponent(canceled ctx) error = %v, want context.Canceled", err)
	}
}

func TestRenderComponentPropagatesRenderError(t *testing.T) {
	want := errors.New("render failed")
	_, err := RenderComponent(context.Background(), failingComponent{err: want})
	if !errors.Is(err, want) {
		t.Errorf("RenderComponent(failing) error = %v, want %v", err, want)
	}
}

// failingComponent always errors, to exercise error propagation.
type failingComponent struct{ err error }

func (f failingComponent) Render(ctx context.Context, w io.Writer) error { return f.err }

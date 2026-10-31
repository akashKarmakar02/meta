package render

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// D09 acceptance: context-safety of components that interpolate user-controlled
// input, through the real templ fixture (templ generates HTML-escaped
// interpolation), and no internal-detail leakage on rendering failures.

func TestUserControlledInputIsEscaped(t *testing.T) {
	hostile := `<script>alert("xss")</script>`
	got, err := RenderComponent(context.Background(), testComponent(hostile))
	if err != nil {
		t.Fatalf("RenderComponent() error = %v", err)
	}
	body := string(got)
	if strings.Contains(body, "<script>") {
		t.Errorf("templ interpolation leaked raw script: %s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Errorf("expected escaped form &lt;script&gt; in: %s", body)
	}
}

func TestAttributeInjectionIsEscaped(t *testing.T) {
	hostile := `" onmouseover="alert(1)`
	got, err := RenderComponent(context.Background(), testComponent(hostile))
	if err != nil {
		t.Fatalf("RenderComponent() error = %v", err)
	}
	body := string(got)
	if strings.Contains(body, `" onmouseover="`) {
		t.Errorf("attribute-context injection survived templ escaping: %s", body)
	}
}

// failingProbe models a render-time failure whose error carries internal details.
type failingProbe struct{ detail string }

func (f failingProbe) Render(_ context.Context, _ io.Writer) error {
	return fmt.Errorf("render failed: %s", f.detail)
}

func TestRenderFailureDoesNotLeakInternals(t *testing.T) {
	detail := "internal stack: goroutine 1 [running], file /home/user/secret.go:42"
	app := fiber.New(fiber.Config{ErrorHandler: HTTPErrorHandler})
	app.Get("/boom", func(c fiber.Ctx) error {
		return fmt.Errorf("render failed: %s", detail)
	})
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/boom", nil))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), "goroutine") || strings.Contains(string(body), "secret.go") {
		t.Errorf("client body leaked internals: %s", body)
	}
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}

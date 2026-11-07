package observability

import (
	"bytes"
	"context"
	"log/slog"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestRequestLoggingAndIsolation(t *testing.T) {
	var logs bytes.Buffer
	metrics := NewCollector()
	app := fiber.New()
	app.Use(Middleware(Config{Logger: slog.New(slog.NewJSONHandler(&logs, nil)), Metrics: metrics}))
	app.Get("/private/:id", func(c fiber.Ctx) error {
		SetRoute(c.Context(), "private/[id]/page.templ")
		Record(c.Context(), "loader", "private/[id]/page.templ", "execute", "ok", 0)
		return c.SendString(RequestID(c.Context()))
	})
	req := httptest.NewRequest("GET", "/private/secret-value?token=secret-query", nil)
	req.Header.Set("X-Request-ID", "untrusted-id")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if id := resp.Header.Get("X-Request-ID"); len(id) != 32 || id == "untrusted-id" {
		t.Fatalf("id=%q", id)
	}
	for _, secret := range []string{"secret-value", "secret-query", "untrusted-id"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("log disclosed %q", secret)
		}
	}
	snap := metrics.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("metrics=%v", snap)
	}
}

func TestDevelopmentLoggerUsesCompactText(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	var output bytes.Buffer
	logger := DevelopmentLogger(&output)
	logger.Info("meta request", "request_id", "request-123", "route", "page.templ", "method", "GET", "status", 200, "duration_ms", 1.23456)
	for _, want := range []string{"INFO", "GET  200  page.templ", "1.235ms", "id=request-123"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("log %q missing %q", output.String(), want)
		}
	}
	if strings.Contains(output.String(), `"time"`) || strings.HasPrefix(output.String(), "{") {
		t.Fatalf("not compact text: %s", output.String())
	}
}

func TestDevelopmentShowsUnmatchedPathsWithoutQueries(t *testing.T) {
	var output bytes.Buffer
	app := fiber.New()
	app.Use(Middleware(Config{Logger: DevelopmentLogger(&output)}))
	for _, path := range []string{"/favicon.ico", "/private-secret?token=secret"} {
		response, err := app.Test(httptest.NewRequest("GET", path, nil))
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
	}
	if !strings.Contains(output.String(), "/favicon.ico") || !strings.Contains(output.String(), "/private-secret (unmatched)") {
		t.Fatalf("logs: %s", output.String())
	}
	if strings.Contains(output.String(), "token=secret") {
		t.Fatalf("query leaked: %s", output.String())
	}
}

func TestProductionHidesUnmatchedPaths(t *testing.T) {
	var output bytes.Buffer
	app := fiber.New()
	app.Use(Middleware(Config{Logger: slog.New(slog.NewJSONHandler(&output, nil))}))
	response, err := app.Test(httptest.NewRequest("GET", "/private-secret?token=secret", nil))
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if strings.Contains(output.String(), "private-secret") || strings.Contains(output.String(), "token=secret") || !strings.Contains(output.String(), "unmatched") {
		t.Fatalf("unexpected production log: %s", output.String())
	}
}

func TestDevelopmentColorCanBeDisabled(t *testing.T) {
	t.Setenv("FORCE_COLOR", "1")
	t.Setenv("NO_COLOR", "1")
	var output bytes.Buffer
	DevelopmentLogger(&output).Info("meta request", "status", 404)
	if strings.Contains(output.String(), "\x1b[") {
		t.Fatalf("color was not disabled: %q", output.String())
	}
}

func TestDevelopmentColorCanBeForced(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	// NO_COLOR takes precedence even if empty; remove it for this test.
	if err := os.Unsetenv("NO_COLOR"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FORCE_COLOR", "1")
	var output bytes.Buffer
	DevelopmentLogger(&output).Info("meta request", "status", 404)
	if !strings.Contains(output.String(), "\x1b[33m404\x1b[0m") {
		t.Fatalf("missing color: %q", output.String())
	}
}

func TestCollectorConcurrentSnapshots(t *testing.T) {
	metrics := NewCollector()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			metrics.Observe(Event{Kind: "http", Route: "page.templ", Outcome: "ok"})
			_ = metrics.Snapshot()
		}()
	}
	wg.Wait()
	snapshot := metrics.Snapshot()
	if len(snapshot) != 1 || snapshot[0].Count != 20 {
		t.Fatalf("%+v", snapshot)
	}
	snapshot[0].Count = 0
	if metrics.Snapshot()[0].Count != 20 {
		t.Fatal("snapshot aliases collector")
	}
	if RequestID(context.Background()) != "" {
		t.Fatal("unexpected request ID")
	}
}

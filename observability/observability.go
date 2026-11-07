// Package observability provides request IDs, structured logs, and optional metrics.
package observability

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
)

// Event contains bounded operational metadata. It never contains request data,
// cache keys, or error strings. Route identifies a registered route or endpoint.
type Event struct {
	Kind      string        `json:"kind"`
	Route     string        `json:"route"`
	Operation string        `json:"operation,omitempty"`
	Outcome   string        `json:"outcome"`
	Status    int           `json:"status,omitempty"`
	Duration  time.Duration `json:"duration_ns"`
}

// Observer receives operational events. Implementations must be concurrency-safe
// and return quickly. Do not use request IDs or raw paths as metric labels.
type Observer interface{ Observe(Event) }

// Config controls request logging and optional metrics.
type Config struct {
	Logger         *slog.Logger
	Metrics        Observer
	DisableLogging bool
}

type contextKey struct{}
type request struct {
	id      string
	logger  *slog.Logger
	metrics Observer
	mu      sync.Mutex
	route   string
}

var defaultLogger = slog.New(slog.NewJSONHandler(os.Stderr, nil))

// RequestID returns the server-generated identifier for this request.
func RequestID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	r, _ := ctx.Value(contextKey{}).(*request)
	if r == nil {
		return ""
	}
	return r.id
}

// SetRoute records a framework-owned route identity for the current request.
func SetRoute(ctx context.Context, route string) {
	if ctx == nil {
		return
	}
	r, _ := ctx.Value(contextKey{}).(*request)
	if r == nil {
		return
	}
	r.mu.Lock()
	r.route = route
	r.mu.Unlock()
}

// Record emits an operation with a fixed outcome. Callers must supply only
// framework-owned route IDs and outcome strings, never user input or errors.
func Record(ctx context.Context, kind, route, operation, outcome string, duration time.Duration) {
	if ctx == nil {
		return
	}
	r, _ := ctx.Value(contextKey{}).(*request)
	if r == nil {
		return
	}
	e := Event{Kind: kind, Route: route, Operation: operation, Outcome: outcome, Duration: duration}
	if r.metrics != nil {
		r.metrics.Observe(e)
	}
	if r.logger != nil {
		r.logger.InfoContext(ctx, "meta operation", "request_id", r.id, "kind", kind, "route", route, "operation", operation, "outcome", outcome, "duration_ms", float64(duration)/float64(time.Millisecond))
	}
}

// Middleware installs a server-generated X-Request-ID and records the request.
// Client-supplied request IDs are deliberately ignored.
func Middleware(cfg Config) fiber.Handler {
	logger := cfg.Logger
	if cfg.DisableLogging {
		logger = nil
	} else if logger == nil {
		logger = defaultLogger
	}
	return func(c fiber.Ctx) (err error) {
		var token [16]byte
		if _, err := rand.Read(token[:]); err != nil {
			return fiber.NewError(http.StatusInternalServerError, "request initialization failed")
		}
		state := &request{id: hex.EncodeToString(token[:]), logger: logger, metrics: cfg.Metrics, route: "unmatched"}
		ctx := context.WithValue(c.Context(), contextKey{}, state)
		c.SetContext(ctx)
		c.Set("X-Request-ID", state.id)
		started := time.Now()
		defer func() {
			recovered := recover()
			status := c.Response().StatusCode()
			if recovered != nil {
				status = http.StatusInternalServerError
			} else if err != nil {
				var fe *fiber.Error
				if errors.As(err, &fe) {
					status = fe.Code
				} else {
					status = http.StatusInternalServerError
				}
			}
			state.mu.Lock()
			route := state.route
			state.mu.Unlock()
			if route == "unmatched" {
				// These fixed browser-discovery paths contain no user input.
				// Keep arbitrary URLs out of logs and metrics.
				switch c.Path() {
				case "/favicon.ico":
					route = "/favicon.ico"
				case "/apple-touch-icon.png":
					route = "/apple-touch-icon.png"
				case "/apple-touch-icon-precomposed.png":
					route = "/apple-touch-icon-precomposed.png"
				case "/robots.txt":
					route = "/robots.txt"
				}
			}
			outcome := "ok"
			if status >= 500 {
				outcome = "error"
			} else if status >= 400 {
				outcome = "rejected"
			}
			e := Event{Kind: "http", Route: route, Outcome: outcome, Status: status, Duration: time.Since(started)}
			if state.metrics != nil {
				state.metrics.Observe(e)
			}
			if state.logger != nil {
				attrs := []any{"request_id", state.id, "route", route, "method", safeMethod(c.Method()), "status", status, "outcome", outcome, "duration_ms", float64(e.Duration) / float64(time.Millisecond)}
				if route == "unmatched" {
					if _, ok := state.logger.Handler().(*developmentHandler); ok {
						attrs = append(attrs, "request_path", boundedPath(c.Path()))
					}
				}
				state.logger.InfoContext(ctx, "meta request", attrs...)
			}
			if recovered != nil {
				panic(recovered)
			}
		}()
		return c.Next()
	}
}

func safeMethod(method string) string {
	switch method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS":
		return method
	default:
		return "OTHER"
	}
}

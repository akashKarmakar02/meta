package observability

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

// DevelopmentLogger formats framework events as readable, single-line logs.
// Other log messages use slog's standard text format.
func DevelopmentLogger(w io.Writer) *slog.Logger {
	return slog.New(&developmentHandler{writer: w, mu: &sync.Mutex{}, fallback: slog.NewTextHandler(w, nil), color: useColor(w)})
}

type developmentHandler struct {
	writer   io.Writer
	mu       *sync.Mutex
	fallback slog.Handler
	attrs    []slog.Attr
	color    bool
}

func (h *developmentHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.fallback.Enabled(ctx, level)
}

func (h *developmentHandler) Handle(ctx context.Context, record slog.Record) error {
	if record.Message != "meta request" && record.Message != "meta operation" {
		return h.fallback.Handle(ctx, record)
	}
	fields := make(map[string]slog.Value)
	for _, attr := range h.attrs {
		fields[attr.Key] = attr.Value.Resolve()
	}
	record.Attrs(func(attr slog.Attr) bool { fields[attr.Key] = attr.Value.Resolve(); return true })
	str := func(key string) string {
		value, ok := fields[key]
		if !ok {
			return "-"
		}
		return safeLogText(value.String())
	}
	var duration float64
	if value, ok := fields["duration_ms"]; ok && value.Kind() == slog.KindFloat64 {
		duration = value.Float64()
	}
	timestamp := record.Time.Format("15:04:05")
	var line string
	if record.Message == "meta request" {
		route := str("route")
		if route == "unmatched" {
			if path, ok := fields["request_path"]; ok {
				route = safeLogText(path.String()) + " (unmatched)"
			}
		}
		status := str("status")
		if h.color {
			switch {
			case strings.HasPrefix(status, "5"):
				status = "\x1b[31m" + status + "\x1b[0m"
			case strings.HasPrefix(status, "4"):
				status = "\x1b[33m" + status + "\x1b[0m"
			case strings.HasPrefix(status, "2"):
				status = "\x1b[32m" + status + "\x1b[0m"
			default:
				status = "\x1b[36m" + status + "\x1b[0m"
			}
		}
		line = fmt.Sprintf("%s  INFO  %s  %s  %s  %.3fms  id=%s\n", timestamp, str("method"), status, route, duration, str("request_id"))
	} else {
		line = fmt.Sprintf("%s  INFO  %s %s  %s  %s  %.3fms  id=%s\n", timestamp, str("kind"), str("operation"), str("route"), str("outcome"), duration, str("request_id"))
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.writer, line)
	return err
}

func (h *developmentHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	combined := append([]slog.Attr(nil), h.attrs...)
	combined = append(combined, attrs...)
	return &developmentHandler{writer: h.writer, mu: h.mu, fallback: h.fallback.WithAttrs(attrs), attrs: combined, color: h.color}
}

func (h *developmentHandler) WithGroup(name string) slog.Handler {
	// Arbitrary grouped logs are rendered by slog's standard text handler.
	return h.fallback.WithGroup(name)
}

func safeLogText(value string) string {
	if value == "" || strings.IndexFunc(value, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.IsSpace(r) }) >= 0 {
		return strconv.Quote(value)
	}
	return value
}

func boundedPath(path string) string {
	runes := []rune(path)
	if len(runes) > 256 {
		return string(runes[:256]) + "…"
	}
	return path
}

func useColor(w io.Writer) bool {
	if _, disabled := os.LookupEnv("NO_COLOR"); disabled {
		return false
	}
	if forced, set := os.LookupEnv("FORCE_COLOR"); set {
		return forced != "0" && forced != ""
	}
	if os.Getenv("GO_META_LOG_COLOR") == "1" {
		return true
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	file, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

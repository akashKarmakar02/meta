package httpserver

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"go-meta/loader"
)

// C05: the error classifier must map every loader outcome to exactly one
// outcome, and wrapped errors must classify by their sentinel (errors.Is).
func TestClassifyLoaderError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want loaderOutcomeClass
	}{
		{"not found", loader.ErrNotFound, outcomeNotFound},
		{"unauthorized", loader.ErrUnauthorized, outcomeUnauthorized},
		{"forbidden", loader.ErrForbidden, outcomeForbidden},
		{"invalid input", loader.ErrInvalidInput, outcomeInvalidInput},
		{"canceled", context.Canceled, outcomeCanceled},
		{"deadline", context.DeadlineExceeded, outcomeDeadline},
		{"redirect", &loader.RedirectError{Location: "/target"}, outcomeRedirect},
		{"internal", errors.New("db connection refused"), outcomeInternal},
		// Wrapped sentinels must classify via errors.Is.
		{"wrapped not found", fmt.Errorf("post %s: %w", "42", loader.ErrNotFound), outcomeNotFound},
		{"wrapped unauthorized", fmt.Errorf("auth check failed: %w", loader.ErrUnauthorized), outcomeUnauthorized},
		{"wrapped forbidden", fmt.Errorf("policy denied: %w", loader.ErrForbidden), outcomeForbidden},
		{"wrapped invalid input", fmt.Errorf("parse team id: %w", loader.ErrInvalidInput), outcomeInvalidInput},
		{"wrapped canceled", fmt.Errorf("fetch aborted: %w", context.Canceled), outcomeCanceled},
		{"wrapped deadline", fmt.Errorf("query slow: %w", context.DeadlineExceeded), outcomeDeadline},
		{"wrapped redirect", fmt.Errorf("login required: %w", &loader.RedirectError{Location: "/login"}), outcomeRedirect},
		// Double-wrapped sentinel.
		{"double wrapped not found", fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", loader.ErrNotFound)), outcomeNotFound},
		// A wrapped non-sentinel error stays internal.
		{"wrapped internal", fmt.Errorf("outer: %w", errors.New("boom")), outcomeInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyLoaderError(tt.err); got != tt.want {
				t.Fatalf("classifyLoaderError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// C05: redirect targets must be root-relative. Anything that could take the
// user off-origin (absolute URL, protocol-relative, javascript:, data:, a
// backslash-sneaked absolute URL) or is not a path (empty, whitespace,
// query-only, fragment-only) must be rejected.
func TestValidRedirectLocation(t *testing.T) {
	valid := []string{
		"/",
		"/login",
		"/dashboard",
		"/users/42/posts",
		"/a/b/c?x=1",
		"/a/b/c#frag",
		"/path with spaces",
		"/%20encoded",
	}
	for _, loc := range valid {
		if !validRedirectLocation(loc) {
			t.Errorf("validRedirectLocation(%q) = false, want true", loc)
		}
	}

	invalid := []string{
		"",
		"   ",
		"login",           // not root-relative
		"//evil.com",      // protocol-relative
		"//",              // protocol-relative root
		"http://evil.com", // absolute
		"https://evil.com/path",
		"javascript:alert(1)",
		"JAVASCRIPT:alert(1)", // case-insensitive scheme
		" data:text/html,x",   // leading whitespace + data:
		"\\evil.com",          // backslash-sneaked absolute
		"\\//evil.com",
		"?query",  // query-only
		"#frag",   // fragment-only
		"/\\evil", // backslash after root is not a path segment separator
		"mailto:x",
		"ftp://x",
	}
	for _, loc := range invalid {
		if validRedirectLocation(loc) {
			t.Errorf("validRedirectLocation(%q) = true, want false", loc)
		}
	}
}

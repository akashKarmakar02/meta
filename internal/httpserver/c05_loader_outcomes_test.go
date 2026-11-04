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

		// C06: percent-encoded bypasses — the browser decodes %XX when
		// resolving Location, so the DECODED form must satisfy every rule
		// too, and ambiguous/invalid escapes fail closed.
		"/%2F%2Fevil.com",                // decodes to //evil.com (protocol-relative)
		"/x%5Cevil.com",                  // %5C is backslash: decodes to /\evil.com
		"/x%5c%5cevil.com",               // lowercase double backslash
		"/a%0D%0Ab: c",                   // %0D%0A decodes to CR/LF (header injection)
		"/a%0d%0ab",                      // lowercase CRLF
		"/x%00y",                         // NUL
		"/a%01b",                         // SOH control character
		"/x%7Fy",                         // DEL (0x7f)
		"https%3A%2F%2Fevil.example.com", // encoded scheme+host (not root-relative anyway)
		"/%zz",                           // invalid percent escape
		"/a%",                            // dangling percent
		"/a%2",                           // truncated escape
		"/%2f%2fevil.com",                // lowercase encoded protocol-relative
	}

	// Decoded forms that remain same-origin paths are still valid: the policy
	// is origin-only, so a decoded "/a//b" (a double slash inside the path) or
	// a decoded dot-segment traversal ("/login/../etc") stays on this origin.
	// Path normalization is the browser's job; the defense boundary here is
	// cross-origin escape, scheme changes, and header injection.
	stillValid := []string{
		"/a%2F%2Fb",                   // decodes to /a//b — odd but same-origin path
		"/login%252F..%252Fevil",      // double-encoded; first decode stays a path char
		"/login/%2e%2e/%2e%2e/etc",    // decoded dot-segment traversal, same-origin
		"/search?q=%2B%20plus%20sign", // encoded + and %20 in a query value
	}
	for _, loc := range stillValid {
		if !validRedirectLocation(loc) {
			t.Errorf("validRedirectLocation(%q) = false, want true", loc)
		}
	}
	for _, loc := range invalid {
		if validRedirectLocation(loc) {
			t.Errorf("validRedirectLocation(%q) = true, want false", loc)
		}
	}
}

package httpserver

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"go-meta/loader"
)

// loaderOutcomeClass names how a loader error maps to an HTTP response.
type loaderOutcomeClass int

const (
	outcomeRedirect loaderOutcomeClass = iota
	outcomeNotFound
	outcomeUnauthorized
	outcomeForbidden
	outcomeInvalidInput
	outcomeCanceled
	outcomeDeadline
	outcomeInternal
)

// classifyLoaderError maps a loader error to its outcome class using
// errors.Is, so errors wrapped around a C01 sentinel (or a context error)
// classify the same as the sentinel itself. Redirect is checked first with
// errors.As because it is a distinct control-flow type, not a sentinel.
func classifyLoaderError(err error) loaderOutcomeClass {
	var redirect *loader.RedirectError
	if errors.As(err, &redirect) {
		return outcomeRedirect
	}
	switch {
	case errors.Is(err, context.Canceled):
		return outcomeCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return outcomeDeadline
	case errors.Is(err, loader.ErrNotFound):
		return outcomeNotFound
	case errors.Is(err, loader.ErrUnauthorized):
		return outcomeUnauthorized
	case errors.Is(err, loader.ErrForbidden):
		return outcomeForbidden
	case errors.Is(err, loader.ErrInvalidInput):
		return outcomeInvalidInput
	default:
		return outcomeInternal
	}
}

// redirectStatus is the status used for loader-requested redirects. 302
// Found is a temporary redirect: the target is the canonical location for
// this request, and the client may cache nothing about the redirect itself.
const redirectStatus = http.StatusFound

// statusClientClosedRequest is the nginx convention for "client closed
// request": the client aborted the request (closed the tab or navigated
// away) while the loader was running. It is not an official IANA status, so
// it is named here rather than written as a bare literal.
const statusClientClosedRequest = 499

// validRedirectLocation reports whether loc is a safe, root-relative redirect
// target. Only same-origin, path-only targets are allowed; anything that could
// be interpreted as an absolute URL, a protocol-relative host, or a different
// scheme is rejected. This is the open-redirect / XSS defense for loader
// redirects: the loader is trusted to choose a destination, but the destination
// must stay within the origin.
//
// Accepted: "/login", "/docs/guide", "/a/b?x=1#top", "/search?q=a%20b".
// Rejected: "" (empty), "login" (relative), "/login/" is fine but "//evil.com"
// (protocol-relative), "http://x" / "https://x" (absolute), "javascript:..."
// and other schemes, "/\evil.com" (backslash normalizes to "//evil.com" in
// browsers), and any value containing a control character (header injection).
//
// C06: every shape rule is applied to BOTH the raw value and its
// percent-decoded form. Browsers decode %XX sequences when resolving the
// Location header, so a raw value that looks safe can still decode into a
// dangerous target: "/%2F%2Fevil.com" decodes to "//evil.com" (protocol
// relative), "/a%5Cb" decodes to a backslash form, and "/x%0D%0Ay" decodes to
// an embedded CR/LF. Invalid percent escapes ("%zz", trailing "%") are
// rejected outright — the generator produced a target the client cannot parse
// unambiguously, so fail closed rather than guess which side wins.
func validRedirectLocation(loc string) bool {
	if !validRedirectShape(loc) {
		return false
	}
	decoded, err := url.PathUnescape(loc)
	if err != nil {
		return false
	}
	return validRedirectShape(decoded)
}

// validRedirectShape applies the structural rules to one spelling of a target.
func validRedirectShape(loc string) bool {
	if loc == "" {
		return false
	}
	// Must be root-relative: a leading "/" rules out relative paths and every
	// scheme-prefixed absolute URL (schemes never start with "/").
	if !strings.HasPrefix(loc, "/") {
		return false
	}
	// Reject protocol-relative targets ("//host/..."): the browser would treat
	// the second slash as the start of an authority and navigate cross-origin.
	if strings.HasPrefix(loc, "//") {
		return false
	}
	// Reject any backslash: browsers normalize "/\host" to "//host", which is
	// a protocol-relative cross-origin navigation.
	if strings.Contains(loc, "\\") {
		return false
	}
	// Reject control characters (including CR/LF) to prevent HTTP header
	// injection through the Location header.
	for _, r := range loc {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

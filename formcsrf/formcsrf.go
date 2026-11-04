// Package formcsrf provides CSRF protection for browser-native HTML form
// submissions to registered actions (D03).
//
// Design: signed double-submit tokens. A page response carries a cookie
// (_meta_csrf) holding a signed token; the server-rendered form embeds the
// same token in a hidden field. A submission is accepted only when the form
// field and cookie hold the same validly-signed, unexpired token.
//
// Threat model: a cross-site page can cause the victim browser to POST any
// urlencoded body to the action endpoint, but it cannot read the token cookie
// (same-origin policy) and cannot forge a token because tokens are HMAC-signed
// with an application-held secret. Cookies additionally carry SameSite=Lax,
// so cross-site POSTs do not even submit the cookie. Signature verification
// prevents an attacker from minting their own nonce and using a matching
// cookie+field pair they control.
//
// The package imports only the standard library plus templ for the render
// helper; it never imports Fiber or fasthttp. Framework code attaches tokens
// to request contexts (WithContext); templ pages read them with FromContext or
// render the hidden field directly with HiddenField.
package formcsrf

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/a-h/templ"
)

// CookieName is the browser cookie holding the signed token.
const CookieName = "_meta_csrf"

// FieldName is the hidden form input carrying the submitted token.
const FieldName = "_csrf"

// TokenTTL bounds how long an issued token stays valid. Tokens are stateless
// (no server-side storage), so revocation before expiry is not possible; the
// short bound limits replay windows. Tokens are not single-use: a page may
// submit its token several times within the TTL, which matches normal form
// usage (retries, multiple forms per page).
const TokenTTL = 24 * time.Hour

// minSecretBytes is the minimum HMAC key length. Shorter secrets are rejected
// at construction rather than silently weakening the tag.
const minSecretBytes = 32

// ErrShortSecret reports a secret below minSecretBytes.
var ErrShortSecret = errors.New("formcsrf: secret must be at least 32 bytes")

// ErrNoToken reports that no CSRF token is available in the render context —
// the framework middleware did not run for this request.
var ErrNoToken = errors.New("formcsrf: no token in context")

// Manager issues and verifies signed double-submit CSRF tokens. It is safe for
// concurrent use. Construct once at startup; the secret never rotates in
// process. Inject a clock for deterministic expiry tests.
type Manager struct {
	secret []byte
	now    func() time.Time
}

// New creates a Manager from an application-held secret of at least 32 bytes.
// now may be nil, meaning time.Now.
func New(secret []byte, now func() time.Time) (*Manager, error) {
	if len(secret) < minSecretBytes {
		return nil, fmt.Errorf("%w: got %d bytes", ErrShortSecret, len(secret))
	}
	if now == nil {
		now = time.Now
	}
	return &Manager{secret: secret, now: now}, nil
}

// Issue returns a fresh signed token for nonce-material generated from
// crypto/rand. The returned token is safe to place in both a cookie value and
// an HTML hidden input attribute.
func (m *Manager) Issue() string {
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		// crypto/rand failure is unrecoverable; fail loudly rather than
		// issue a predictable token.
		panic("formcsrf: entropy source failed: " + err.Error())
	}
	return m.sign(nonce, m.now())
}

// Sign returns the token for an existing nonce (used by the framework to
// re-sign a cookie's nonce on later page views without rotating it). The
// issued-at is stamped now; a nonce carried forward past TokenTTL ages out and
// the middleware re-issues.
func (m *Manager) Sign(nonce []byte) string {
	return m.sign(nonce, m.now())
}

// Verify accepts a submission only when the form field token and cookie token
// are identical, carry a valid HMAC under this manager's secret, and were
// issued within TokenTTL. Any tampering, mismatch, truncation, or expiry fails
// closed.
func (m *Manager) Verify(formToken, cookieToken string) bool {
	if formToken == "" || cookieToken == "" || formToken != cookieToken {
		return false
	}
	payload, sig, ok := splitToken(formToken)
	if !ok || !hmac.Equal(sig, m.mac(payload)) {
		return false
	}
	nonce, issued, ok := decodePayload(payload)
	if !ok || len(nonce) != 32 {
		return false
	}
	age := m.now().Sub(issued)
	return age >= 0 && age <= TokenTTL
}

// ValidToken reports whether token parses and verifies under this manager,
// ignoring the double-submit comparison. Used by the issuance middleware to
// decide whether a browser's existing cookie can be kept or must be rotated.
func (m *Manager) ValidToken(token string) bool {
	payload, sig, ok := splitToken(token)
	if !ok || !hmac.Equal(sig, m.mac(payload)) {
		return false
	}
	nonce, issued, ok := decodePayload(payload)
	if !ok || len(nonce) != 32 {
		return false
	}
	age := m.now().Sub(issued)
	return age >= 0 && age <= TokenTTL
}

func (m *Manager) sign(nonce []byte, at time.Time) string {
	payload := encodePayload(nonce, at)
	sig := m.mac(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(sig)
}

func (m *Manager) mac(payload []byte) []byte {
	h := hmac.New(sha256.New, m.secret)
	h.Write(payload)
	return h.Sum(nil)
}

func splitToken(token string) (payload, sig []byte, ok bool) {
	dot := -1
	for i := len(token) - 1; i >= 0; i-- {
		if token[i] == '.' {
			dot = i
			break
		}
	}
	if dot <= 0 || dot == len(token)-1 {
		return nil, nil, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(token[:dot])
	if err != nil {
		return nil, nil, false
	}
	sig, err = base64.RawURLEncoding.DecodeString(token[dot+1:])
	if err != nil {
		return nil, nil, false
	}
	return payload, sig, true
}

// payload layout: 32-byte nonce + 8-byte big-endian Unix seconds.
func encodePayload(nonce []byte, at time.Time) []byte {
	p := make([]byte, 0, 40)
	p = append(p, nonce...)
	unix := at.Unix()
	for shift := 56; shift >= 0; shift -= 8 {
		p = append(p, byte(unix>>shift))
	}
	return p
}

func decodePayload(payload []byte) (nonce []byte, issued time.Time, ok bool) {
	if len(payload) != 40 {
		return nil, time.Time{}, false
	}
	var unix int64
	for _, b := range payload[32:] {
		unix = unix<<8 | int64(b)
	}
	return payload[:32], time.Unix(unix, 0), true
}

// tokenKey is the private render-context key for the current request's token.
type tokenKey struct{}

// WithContext returns a copy of ctx carrying the page's CSRF token. It is the
// framework-facing attachment point; application code reads tokens with
// FromContext or HiddenField, never re-attaching them.
func WithContext(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, tokenKey{}, token)
}

// FromContext returns the CSRF token for the current render context. ok is
// false when the framework did not attach one (middleware not mounted): a
// page rendering a form should treat that as an error, because the submission
// would be rejected.
func FromContext(ctx context.Context) (token string, ok bool) {
	if ctx == nil {
		return "", false
	}
	t, ok := ctx.Value(tokenKey{}).(string)
	return t, ok && t != ""
}

// HiddenField renders the hidden input carrying this request's token. When no
// token is attached it renders an HTML comment instead of an empty field, so
// an unconfigured deployment fails visibly (its submissions are rejected with
// 403) rather than silently producing tokenless forms.
//
// It takes no arguments on purpose: templ passes the page's render context to
// child components automatically, so `@formcsrf.HiddenField()` inside a .templ
// file picks up the framework-attached token.
func HiddenField() templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		token, ok := FromContext(ctx)
		if !ok {
			_, err := io.WriteString(w, "<!-- formcsrf: no token in context -->")
			return err
		}
		if _, err := io.WriteString(w, `<input type="hidden" name="`+FieldName+`" value="`+templ.EscapeString(token)+`">`); err != nil {
			return err
		}
		return nil
	})
}

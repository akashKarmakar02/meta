package metaclerk

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
)

const recoveryCookie = "__meta_clerk_recovery"

// Recovery is public routing data, not proof of authentication. A loader can
// pass it to a Svelte page as its data prop, including during soft navigation.
type Recovery struct {
	ReturnTo      string `json:"returnTo"`
	SignInURL     string `json:"signInURL"`
	RecoveryURL   string `json:"recoveryURL"`
	RetryRequired bool   `json:"retryRequired"`
}

type recoveryKey struct{}

// RecoveryFromContext returns validated data installed by RecoveryPage.
func RecoveryFromContext(ctx context.Context) (Recovery, bool) {
	if ctx == nil {
		return Recovery{}, false
	}
	value, ok := ctx.Value(recoveryKey{}).(Recovery)
	return value, ok
}

// localDestination rejects browser URL ambiguities before using a destination
// in a Location header. It never derives an origin from request Host headers.
func localDestination(raw string) (*url.URL, bool) {
	if len(raw) == 0 || len(raw) > 4096 || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return nil, false
	}
	for _, ch := range raw {
		if ch <= 0x20 || ch == 0x7f || ch == '\\' {
			return nil, false
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" || u.User != nil || strings.HasPrefix(u.Path, "//") {
		return nil, false
	}
	for _, ch := range u.Path {
		if ch <= 0x20 || ch == 0x7f || ch == '\\' || ch == '%' {
			return nil, false
		}
	}
	clean := path.Clean(u.Path)
	if strings.HasSuffix(u.Path, "/") && clean != "/" {
		clean += "/"
	}
	u.Path, u.RawPath = clean, ""
	return u, true
}

func (a *Integration) returnTo(raw string) (string, bool) {
	u, ok := localDestination(raw)
	if !ok {
		return "", false
	}
	signIn, _ := url.Parse(a.signInURL) // New validated this trusted configuration.
	if strings.TrimRight(u.Path, "/") == strings.TrimRight(signIn.Path, "/") ||
		(a.recoveryURL != "" && strings.TrimRight(u.Path, "/") == strings.TrimRight(a.recoveryURL, "/")) {
		return "", false
	}
	return u.String(), true
}

func attemptValue(destination string) string {
	digest := sha256.Sum256([]byte(destination))
	return hex.EncodeToString(digest[:])
}

func (a *Integration) clearRecoveryAttempt(c fiber.Ctx) {
	if c.Cookies(recoveryCookie) == "" {
		return
	}
	c.Cookie(&fiber.Cookie{
		Name: recoveryCookie, Value: "", Path: "/", HTTPOnly: true,
		Secure: c.Secure(), SameSite: "Lax", MaxAge: -1, Expires: time.Unix(1, 0),
	})
}

func (a *Integration) signInDestination(destination string) string {
	u, _ := url.Parse(a.signInURL)
	query := u.Query()
	query.Set("redirect_url", destination)
	u.RawQuery = query.Encode()
	return u.String()
}

func (a *Integration) redirectPage(c fiber.Ctx) error {
	destination, ok := a.returnTo(c.OriginalURL())
	if !ok {
		return c.SendStatus(fiber.StatusUnauthorized)
	}
	event, _ := c.Context().Value(authenticationKey{}).(AuthenticationEvent)
	// Bearer callers and invalid signatures/claims are never browser recovery.
	if event.Source == "authorization" || (event.Reason != AuthMissing && event.Reason != AuthExpired) {
		return c.Redirect().Status(fiber.StatusSeeOther).To(a.signInDestination(destination))
	}
	query := url.Values{"return_to": {destination}}
	if c.Cookies(recoveryCookie) == attemptValue(destination) {
		// Navigation-only loop guard, not an authentication/refresh credential.
		// The public page must require an explicit retry instead of auto-resuming.
		query.Set("retry", "1")
	} else {
		c.Cookie(&fiber.Cookie{
			Name: recoveryCookie, Value: attemptValue(destination), Path: "/",
			HTTPOnly: true, Secure: c.Secure(), SameSite: "Lax",
			MaxAge: 120, Expires: time.Now().Add(2 * time.Minute),
		})
	}
	return c.Redirect().Status(fiber.StatusSeeOther).To(a.recoveryURL + "?" + query.Encode())
}

// RecoveryPage prepares an application-owned PUBLIC page. Mount it only at
// Config.RecoveryURL, after Middleware and before Meta route dispatch. The
// application renders loading/failure/retry UI; this handler emits no HTML/JS.
func (a *Integration) RecoveryPage() fiber.Handler {
	return func(c fiber.Ctx) error {
		c.Set(fiber.HeaderCacheControl, "private, no-store")
		if a.recoveryURL == "" || c.Path() != a.recoveryURL {
			return c.SendStatus(fiber.StatusNotFound)
		}
		if c.Method() != fiber.MethodGet && c.Method() != fiber.MethodHead {
			return c.SendStatus(fiber.StatusMethodNotAllowed)
		}
		destination, ok := a.returnTo(c.Query("return_to"))
		if !ok {
			return c.Status(fiber.StatusBadRequest).SendString("Invalid authentication destination")
		}
		data := Recovery{
			ReturnTo: destination, SignInURL: a.signInDestination(destination), RecoveryURL: a.recoveryURL,
			RetryRequired: c.Query("retry") == "1",
		}
		c.SetContext(context.WithValue(c.Context(), recoveryKey{}, data))
		return c.Next()
	}
}

// RecoveryStatus confirms a freshly verified COOKIE session after ClerkJS
// refreshes it. Mount a public GET endpoint after Middleware. It returns no
// identity and never mints tokens, changes __session, or accepts bearer-only
// authentication as proof that the browser cookie is ready for SSR.
func (a *Integration) RecoveryStatus() fiber.Handler {
	return func(c fiber.Ctx) error {
		c.Set(fiber.HeaderCacheControl, "private, no-store")
		if c.Method() != fiber.MethodGet && c.Method() != fiber.MethodHead {
			return c.SendStatus(fiber.StatusMethodNotAllowed)
		}
		event, _ := c.Context().Value(authenticationKey{}).(AuthenticationEvent)
		if _, ok := SessionFromContext(c.Context()); !ok || event.Source != "cookie" || event.Reason != AuthVerified {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "authentication required"})
		}
		return c.SendStatus(fiber.StatusNoContent)
	}
}

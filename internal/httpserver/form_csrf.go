package httpserver

import (
	"github.com/gofiber/fiber/v3"

	"go-meta/formcsrf"
)

// WithFormCSRF issues the double-submit CSRF material for browser-native form
// pages (D03). It runs once per GET page response that may render a form:
//
//   - The request context gains a signed token (formcsrf.WithContext) so templ
//     pages can render formcsrf.HiddenField(ctx).
//   - The response gains a _meta_csrf cookie carrying the same token
//     (SameSite=Lax, HttpOnly off — it need not be secret from the page's own
//     JS; the field and cookie must match, and only same-origin JS or HTML can
//     read either).
//
// A browser with a still-valid cookie token keeps it (no rotation on every page
// view, so several tabs and Back/Forward keep working); an absent or stale
// cookie gets a fresh token. Tokens carry a 24h TTL inside their signature.
//
// Only page GETs pass through here; action submissions verify on POST via the
// manager directly and never re-issue through this middleware.
func WithFormCSRF(manager *formcsrf.Manager) fiber.Handler {
	return func(c fiber.Ctx) error {
		cookieToken := c.Cookies(formcsrf.CookieName)
		token := cookieToken
		if !manager.ValidToken(token) {
			token = manager.Issue()
			c.Cookie(&fiber.Cookie{
				Name:     formcsrf.CookieName,
				Value:    token,
				Path:     "/",
				SameSite: fiber.CookieSameSiteLaxMode,
				MaxAge:   int(formcsrf.TokenTTL.Seconds()),
			})
		}
		c.SetContext(formcsrf.WithContext(c.Context(), token))
		return c.Next()
	}
}

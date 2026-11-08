// Package metaclerk integrates Clerk sessions and webhooks with Fiber and Meta.
package metaclerk

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	clerk "github.com/clerk/clerk-sdk-go/v2"
	"github.com/clerk/clerk-sdk-go/v2/jwks"
	"github.com/clerk/clerk-sdk-go/v2/jwt"
	"github.com/gofiber/fiber/v3"
	svix "github.com/svix/svix-webhooks/go"
)

// Config contains server-only credentials and trusted frontend origins.
// AuthorizedParties must contain the exact origins of the application's frontends.
type Config struct {
	SecretKey         string
	SigningSecret     string
	SignInURL         string
	AuthorizedParties []string
}

type Integration struct {
	signInURL   string
	parties     map[string]struct{}
	keys        *jwks.Client
	webhook     *svix.Webhook
	mu          sync.Mutex
	cached      map[string]*clerk.JSONWebKey
	fetched     time.Time
	lastAttempt time.Time
}

// New validates configuration; the signing secret is optional unless Webhook is used.
func New(cfg Config) (*Integration, error) {
	if strings.TrimSpace(cfg.SecretKey) == "" {
		return nil, errors.New("metaclerk: secret key is required")
	}
	if len(cfg.AuthorizedParties) == 0 {
		return nil, errors.New("metaclerk: at least one authorized party is required")
	}
	parties := make(map[string]struct{}, len(cfg.AuthorizedParties))
	for _, party := range cfg.AuthorizedParties {
		u, err := url.Parse(party)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return nil, fmt.Errorf("metaclerk: invalid authorized party %q", party)
		}
		parties[party] = struct{}{}
	}
	if cfg.SignInURL == "" {
		cfg.SignInURL = "/sign-in"
	}
	u, err := url.Parse(cfg.SignInURL)
	if err != nil || !strings.HasPrefix(cfg.SignInURL, "/") || strings.HasPrefix(cfg.SignInURL, "//") || strings.HasPrefix(u.Path, "//") || u.IsAbs() || u.Host != "" || strings.ContainsAny(u.Path, "\\\r\n") || strings.ContainsAny(cfg.SignInURL, "\\\r\n") {
		return nil, errors.New("metaclerk: sign-in URL must be a local path")
	}
	var hook *svix.Webhook
	if cfg.SigningSecret != "" {
		hook, err = svix.NewWebhook(cfg.SigningSecret)
		if err != nil {
			return nil, fmt.Errorf("metaclerk: invalid webhook signing secret: %w", err)
		}
	}
	return &Integration{signInURL: cfg.SignInURL, parties: parties, keys: jwks.NewClient(&clerk.ClientConfig{BackendConfig: clerk.BackendConfig{Key: clerk.String(cfg.SecretKey)}}), webhook: hook}, nil
}

type sessionKey struct{}

// Session contains the verified identity; it does not imply authorization.
type Session struct {
	UserID    string
	SessionID string
}

// SessionFromContext returns the verified request identity, if present.
func SessionFromContext(ctx context.Context) (Session, bool) {
	if ctx == nil {
		return Session{}, false
	}
	s, ok := ctx.Value(sessionKey{}).(Session)
	return s, ok
}

// Middleware reads and verifies the session without requiring a user.
// Protected and personalized responses must also disable shared caching.
func (a *Integration) Middleware() fiber.Handler {
	return func(c fiber.Ctx) error {
		token := ""
		if h := c.Get(fiber.HeaderAuthorization); h != "" {
			fields := strings.Fields(h)
			if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") {
				return c.Next()
			}
			token = fields[1]
		} else {
			token = c.Cookies("__session")
		}
		if token == "" || len(token) > 16384 {
			return c.Next()
		}
		ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
		defer cancel()
		decoded, err := jwt.Decode(ctx, &jwt.DecodeParams{Token: token})
		if err != nil || decoded.KeyID == "" || len(decoded.KeyID) > 256 {
			return c.Next()
		}
		key, err := a.key(ctx, decoded.KeyID)
		if err != nil {
			return c.Status(fiber.StatusServiceUnavailable).SendString("Authentication temporarily unavailable")
		}
		if key == nil {
			return c.Next()
		}
		claims, err := jwt.Verify(ctx, &jwt.VerifyParams{Token: token, JWK: key, AuthorizedPartyHandler: func(p string) bool { _, ok := a.parties[p]; return ok }})
		if err != nil || claims.Subject == "" || claims.SessionID == "" || claims.Expiry == nil {
			return c.Next()
		}
		c.Set(fiber.HeaderCacheControl, "private, no-store")
		c.SetContext(context.WithValue(c.Context(), sessionKey{}, Session{UserID: claims.Subject, SessionID: claims.SessionID}))
		return c.Next()
	}
}

func (a *Integration) key(ctx context.Context, id string) (*clerk.JSONWebKey, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	if k := a.cached[id]; k != nil && now.Sub(a.fetched) < time.Hour {
		return k, nil
	}
	// Bound refreshes for attacker-controlled unknown key IDs and failed requests.
	if !a.lastAttempt.IsZero() && now.Sub(a.lastAttempt) < 30*time.Second {
		if a.fetched.IsZero() || now.Sub(a.fetched) >= time.Hour {
			return nil, errors.New("signing keys unavailable")
		}
		return nil, nil
	}
	a.lastAttempt = now
	set, err := a.keys.Get(ctx, &jwks.GetParams{})
	if err != nil {
		return nil, err
	}
	keys := make(map[string]*clerk.JSONWebKey, len(set.Keys))
	for _, key := range set.Keys {
		if key != nil && key.KeyID != "" {
			keys[key.KeyID] = key
		}
	}
	a.cached, a.fetched = keys, now
	return keys[id], nil
}

// RequireUser protects API routes and actions with an HTTP 401 response.
func (a *Integration) RequireUser() fiber.Handler {
	return func(c fiber.Ctx) error {
		c.Set(fiber.HeaderCacheControl, "private, no-store")
		if _, ok := SessionFromContext(c.Context()); !ok {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "authentication required"})
		}
		return c.Next()
	}
}

// RequirePageUser redirects unauthenticated GET and HEAD page requests.
// For other methods it returns 401 so requests and mutations are never redirected.
func (a *Integration) RequirePageUser() fiber.Handler {
	return func(c fiber.Ctx) error {
		c.Set(fiber.HeaderCacheControl, "private, no-store")
		if _, ok := SessionFromContext(c.Context()); !ok {
			if c.Method() == fiber.MethodGet || c.Method() == fiber.MethodHead {
				return c.Redirect().Status(fiber.StatusSeeOther).To(a.signInURL)
			}
			return c.SendStatus(fiber.StatusUnauthorized)
		}
		return c.Next()
	}
}

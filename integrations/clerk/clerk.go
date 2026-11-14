// Package metaclerk integrates Clerk sessions and webhooks with Fiber and Meta.
package metaclerk

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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
	Retry             RetryConfig
	// HTTPClient is optional. New copies it; configure it before calling New.
	// Its transport must honor request contexts and must not perform retries.
	HTTPClient *http.Client
	// OnKeyFetch receives safe attempt and sequence diagnostics. It runs
	// synchronously, outside the cache lock, and must be fast and concurrency-safe.
	// The context carries the initiating request's correlation values.
	OnKeyFetch func(context.Context, KeyFetchEvent)
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
	refresh     *keyRefresh
	retry       RetryConfig
	onKeyFetch  func(context.Context, KeyFetchEvent)
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
	retry, err := normalizeRetry(cfg.Retry)
	if err != nil {
		return nil, err
	}
	var hook *svix.Webhook
	if cfg.SigningSecret != "" {
		hook, err = svix.NewWebhook(cfg.SigningSecret)
		if err != nil {
			return nil, fmt.Errorf("metaclerk: invalid webhook signing secret: %w", err)
		}
	}
	return &Integration{
		signInURL: cfg.SignInURL, parties: parties, webhook: hook,
		retry: retry, onKeyFetch: cfg.OnKeyFetch,
		keys: jwks.NewClient(&clerk.ClientConfig{BackendConfig: clerk.BackendConfig{
			Key: clerk.String(cfg.SecretKey), HTTPClient: keyHTTPClient(cfg.HTTPClient),
		}}),
	}, nil
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
		ctx := c.Context()
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

// keyRefresh publishes one sequence's result to all waiting callers.
// Closing done synchronizes access to keys and err.
type keyRefresh struct {
	done chan struct{}
	keys map[string]*clerk.JSONWebKey
	err  error
}

func (a *Integration) key(ctx context.Context, id string) (*clerk.JSONWebKey, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.mu.Lock()
	now := time.Now()
	if k := a.cached[id]; k != nil && now.Sub(a.fetched) < time.Hour {
		a.mu.Unlock()
		return k, nil
	}
	if active := a.refresh; active != nil {
		a.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-active.done:
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return active.keys[id], active.err
		}
	}
	// Bound sequences for attacker-controlled unknown key IDs and failed requests.
	// This cooldown never applies to attempts within a sequence.
	if !a.lastAttempt.IsZero() && now.Sub(a.lastAttempt) < 30*time.Second {
		unavailable := a.fetched.IsZero() || now.Sub(a.fetched) >= time.Hour
		a.mu.Unlock()
		if unavailable {
			return nil, errSigningKeysUnavailable
		}
		return nil, nil
	}
	active := &keyRefresh{done: make(chan struct{})}
	a.refresh, a.lastAttempt = active, now
	a.mu.Unlock()

	keys, err := a.fetchKeys(ctx)
	a.mu.Lock()
	if err == nil {
		a.cached, a.fetched = keys, time.Now()
	}
	active.keys, active.err = keys, err
	a.refresh = nil
	close(active.done)
	a.mu.Unlock()
	return keys[id], err
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

package metaclerk

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/clerk/clerk-sdk-go/v2/jwt"
	"github.com/go-jose/go-jose/v4"
	josejwt "github.com/go-jose/go-jose/v4/jwt"
	"github.com/gofiber/fiber/v3"
)

// AuthenticationReason is a bounded server diagnostic, never a raw SDK error.
type AuthenticationReason string

const (
	AuthVerified         AuthenticationReason = "verified"
	AuthMissing          AuthenticationReason = "missing_credentials"
	AuthMalformed        AuthenticationReason = "malformed_credentials"
	AuthExpired          AuthenticationReason = "expired_token"
	AuthInvalidSignature AuthenticationReason = "invalid_signature"
	AuthInvalidClaims    AuthenticationReason = "invalid_claims"
	AuthUnknownKey       AuthenticationReason = "unknown_signing_key"
	AuthKeysUnavailable  AuthenticationReason = "signing_keys_unavailable"
)

// AuthenticationEvent excludes identifiers, token claims, cookies and raw errors.
// Duration covers credential verification, including any signing-key retries.
// Source is none, cookie, or authorization. Expired means signature verification
// reached the SDK's expiry check, not that all subsequent claim checks passed.
type AuthenticationEvent struct {
	Reason   AuthenticationReason
	Source   string
	Duration time.Duration
}

type authenticationKey struct{}

func (a *Integration) authenticate(c fiber.Ctx) error {
	started := time.Now()
	source, token := "cookie", ""
	finish := func(reason AuthenticationReason) {
		event := AuthenticationEvent{Reason: reason, Source: source, Duration: time.Since(started)}
		c.SetContext(context.WithValue(c.Context(), authenticationKey{}, event))
		if a.onAuthentication != nil {
			a.onAuthentication(c.Context(), event)
		}
	}
	if header := c.Get(fiber.HeaderAuthorization); header != "" {
		source = "authorization"
		fields := strings.Fields(header)
		if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") {
			finish(AuthMalformed)
			return c.Next()
		}
		token = fields[1]
	} else {
		token = c.Cookies("__session")
	}
	if token == "" {
		source = "none"
		finish(AuthMissing)
		return c.Next()
	}
	if len(token) > 16384 {
		finish(AuthMalformed)
		return c.Next()
	}
	ctx := c.Context()
	// Decode is used ONLY for bounded signing-key lookup, never identity/expiry.
	decoded, err := jwt.Decode(ctx, &jwt.DecodeParams{Token: token})
	if err != nil || decoded.KeyID == "" || len(decoded.KeyID) > 256 {
		finish(AuthMalformed)
		return c.Next()
	}
	key, err := a.key(ctx, decoded.KeyID)
	if err != nil {
		finish(AuthKeysUnavailable)
		c.Set(fiber.HeaderCacheControl, "private, no-store")
		return c.Status(fiber.StatusServiceUnavailable).SendString("Authentication temporarily unavailable")
	}
	if key == nil {
		finish(AuthUnknownKey)
		return c.Next()
	}
	claims, err := jwt.Verify(ctx, &jwt.VerifyParams{
		Token:                  token,
		JWK:                    key,
		AuthorizedPartyHandler: func(party string) bool { _, ok := a.parties[party]; return ok },
	})
	if err != nil {
		reason := AuthInvalidClaims
		switch {
		case errors.Is(err, jose.ErrCryptoFailure):
			reason = AuthInvalidSignature
		case errors.Is(err, josejwt.ErrExpired):
			reason = AuthExpired
		}
		finish(reason)
		return c.Next()
	}
	if claims.Subject == "" || claims.SessionID == "" || claims.Expiry == nil {
		finish(AuthInvalidClaims)
		return c.Next()
	}
	if *claims.Expiry <= time.Now().Unix() {
		finish(AuthExpired)
		return c.Next()
	}
	c.Set(fiber.HeaderCacheControl, "private, no-store")
	c.SetContext(context.WithValue(ctx, sessionKey{}, Session{UserID: claims.Subject, SessionID: claims.SessionID}))
	finish(AuthVerified)
	return c.Next()
}

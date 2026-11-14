package metaclerk

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"time"

	clerk "github.com/clerk/clerk-sdk-go/v2"
	"github.com/clerk/clerk-sdk-go/v2/jwks"
)

// RetryConfig controls a single signing-key fetch sequence. Each zero field
// selects its default: 3 retries, 100ms initial backoff, 1s maximum backoff,
// 1s attempt timeout, and 5s total timeout. MaxRetries = -1 disables retries
// (one attempt, still bounded by both timeouts). Other negative values are invalid.
// Backoff doubles with 80–120% jitter, capped at MaxBackoff. Retry-After can
// exceed MaxBackoff, but never the remaining total or caller budget.
type RetryConfig struct {
	MaxRetries     int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	AttemptTimeout time.Duration
	TotalTimeout   time.Duration
}

// KeyFetchEvent is a server-side diagnostic with no credentials or raw errors.
// Final=false describes one attempt (Duration is that attempt's duration).
// Final=true summarizes the sequence (Attempt is the number of attempts,
// Duration includes backoff). StatusCode is zero when no response was received.
// Category is success, http, connection, timeout, canceled, permanent,
// invalid_response, or budget_exhausted. Success describes key fetching only;
// the session must still pass token verification.
type KeyFetchEvent struct {
	Attempt    int
	Final      bool
	Success    bool
	Category   string
	StatusCode int
	Duration   time.Duration
}

var errSigningKeysUnavailable = errors.New("signing keys unavailable")

func normalizeRetry(r RetryConfig) (RetryConfig, error) {
	if r.MaxRetries < -1 || r.InitialBackoff < 0 || r.MaxBackoff < 0 || r.AttemptTimeout < 0 || r.TotalTimeout < 0 {
		return r, errors.New("metaclerk: retry counts and durations must be nonnegative (MaxRetries may be -1)")
	}
	if r.MaxRetries == 0 {
		r.MaxRetries = 3
	}
	if r.InitialBackoff == 0 {
		r.InitialBackoff = 100 * time.Millisecond
	}
	if r.MaxBackoff == 0 {
		r.MaxBackoff = time.Second
	}
	if r.AttemptTimeout == 0 {
		r.AttemptTimeout = time.Second
	}
	if r.TotalTimeout == 0 {
		r.TotalTimeout = 5 * time.Second
	}
	if r.MaxBackoff < r.InitialBackoff {
		return r, errors.New("metaclerk: maximum backoff must be at least initial backoff")
	}
	return r, nil
}

// The SDK discards status/headers for some errors and can return raw bodies.
// Intercept failed responses before its decoder; retain only safe metadata.
type keyAttempt struct {
	status     int
	retryAfter string
}
type keyAttemptContextKey struct{}
type keyTransport struct{ base http.RoundTripper }
type keyStatusError struct{ status int }

func (e *keyStatusError) Error() string { return "signing-key HTTP failure" }

func (t keyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if resp != nil {
		if meta, ok := req.Context().Value(keyAttemptContextKey{}).(*keyAttempt); ok {
			meta.status = resp.StatusCode
			if resp.StatusCode == 429 || resp.StatusCode == 503 {
				meta.retryAfter = resp.Header.Get("Retry-After")
			}
		}
		if err != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
			if resp.Body != nil {
				// Do not read untrusted error bodies; closing also prevents a stalled
				// error body from consuming the budget before the next attempt.
				resp.Body.Close()
			}
			if err == nil {
				err = &keyStatusError{status: resp.StatusCode}
			}
			return nil, err
		}
	}
	return resp, err
}

func keyHTTPClient(configured *http.Client) *http.Client {
	client := &http.Client{}
	if configured != nil {
		*client = *configured
	}
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	client.Transport = keyTransport{base: base}
	// JWKS must never redirect to a different endpoint or forward credentials.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client
}

func (a *Integration) report(ctx context.Context, event KeyFetchEvent) {
	if a.onKeyFetch != nil {
		a.onKeyFetch(ctx, event)
	}
}

func (a *Integration) fetchKeys(parent context.Context) (keys map[string]*clerk.JSONWebKey, err error) {
	ctx, cancel := context.WithTimeout(parent, a.retry.TotalTimeout)
	defer cancel()
	started := time.Now()
	event := KeyFetchEvent{}
	defer func() {
		event.Final, event.Success, event.Duration = true, err == nil, time.Since(started)
		a.report(ctx, event)
	}()
	backoff := a.retry.InitialBackoff
	for {
		if ctx.Err() != nil {
			event.Category = contextCategory(ctx.Err())
			return nil, ctx.Err()
		}
		event.Attempt++
		attemptStarted := time.Now()
		attemptCtx, attemptCancel := context.WithTimeout(ctx, a.retry.AttemptTimeout)
		meta := &keyAttempt{}
		set, attemptErr := a.keys.Get(context.WithValue(attemptCtx, keyAttemptContextKey{}, meta), &jwks.GetParams{})
		if attemptCtx.Err() != nil {
			attemptErr = attemptCtx.Err()
		}
		attemptCancel()
		event.StatusCode = meta.status
		retryable := false
		event.Category = "success"
		if attemptErr == nil {
			keys, attemptErr = validatedKeys(set)
			if attemptErr != nil {
				event.Category = "invalid_response"
			}
		} else {
			event.Category, retryable = classifyKeyFailure(attemptErr, meta.status)
		}
		event.Success, event.Duration = attemptErr == nil, time.Since(attemptStarted)
		a.report(ctx, event)
		if ctx.Err() != nil {
			event.Category = contextCategory(ctx.Err())
			return nil, ctx.Err()
		}
		if attemptErr == nil {
			return keys, nil
		}
		if !retryable || event.Attempt > a.retry.MaxRetries {
			return nil, errSigningKeysUnavailable
		}
		delay := jitteredBackoff(backoff, a.retry.MaxBackoff)
		if required := retryAfterDelay(meta.retryAfter, time.Now()); required > delay {
			delay = required
		}
		deadline, _ := ctx.Deadline()
		if delay >= time.Until(deadline) {
			event.Category = "budget_exhausted"
			return nil, errSigningKeysUnavailable
		}
		if waitErr := waitForRetry(ctx, delay); waitErr != nil {
			event.Category = contextCategory(waitErr)
			return nil, waitErr
		}
		if backoff > a.retry.MaxBackoff/2 {
			backoff = a.retry.MaxBackoff
		} else {
			backoff *= 2
		}
	}
}

func validatedKeys(set *clerk.JSONWebKeySet) (map[string]*clerk.JSONWebKey, error) {
	if set == nil || len(set.Keys) == 0 {
		return nil, errSigningKeysUnavailable
	}
	keys := make(map[string]*clerk.JSONWebKey, len(set.Keys))
	for _, key := range set.Keys {
		if key == nil || key.Key == nil || key.KeyID == "" || key.Algorithm == "" {
			return nil, errSigningKeysUnavailable
		}
		if _, duplicate := keys[key.KeyID]; duplicate {
			return nil, errSigningKeysUnavailable
		}
		keys[key.KeyID] = key
	}
	return keys, nil
}

func contextCategory(err error) string {
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	return "timeout"
}

func classifyKeyFailure(err error, status int) (string, bool) {
	if errors.Is(err, context.Canceled) {
		return "canceled", false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout", true
	}
	var httpErr *keyStatusError
	if errors.As(err, &httpErr) {
		switch httpErr.status {
		case 429, 500, 502, 503, 504:
			return "http", true
		default:
			return "http", false
		}
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "connection", dnsErr.IsTimeout || dnsErr.IsTemporary
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout", true
	}
	for _, transient := range []error{io.EOF, io.ErrUnexpectedEOF, syscall.ECONNRESET, syscall.ECONNREFUSED, syscall.ECONNABORTED, syscall.EPIPE, syscall.EHOSTUNREACH, syscall.ENETUNREACH} {
		if errors.Is(err, transient) {
			return "connection", true
		}
	}
	if status >= 200 && status < 300 {
		return "invalid_response", false
	}
	return "permanent", false
}

func jitteredBackoff(base, maximum time.Duration) time.Duration {
	// Clamp before converting to avoid overflow at very large configured values.
	jitter := 0.8 + rand.Float64()*0.4
	if float64(base)*jitter >= float64(maximum) {
		return maximum
	}
	return time.Duration(float64(base) * jitter)
}

func retryAfterDelay(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	// Saturate arbitrarily large delta-seconds instead of retrying early.
	digits := true
	for _, c := range value {
		if c < '0' || c > '9' {
			digits = false
			break
		}
	}
	if digits {
		seconds, err := strconv.ParseUint(value, 10, 64)
		const maxDuration = time.Duration(1<<63 - 1)
		if err != nil || seconds > uint64(maxDuration/time.Second) {
			return maxDuration
		}
		return time.Duration(seconds) * time.Second
	}
	if date, err := http.ParseTime(value); err == nil && date.After(now) {
		return date.Sub(now)
	}
	return 0
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}

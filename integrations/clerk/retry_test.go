package metaclerk

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/go-jose/go-jose/v4"
	josejwt "github.com/go-jose/go-jose/v4/jwt"
	"github.com/gofiber/fiber/v3"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func testConfig() Config {
	return Config{SecretKey: "sk_test_do_not_log", AuthorizedParties: []string{"https://example.com"}}
}

func newTestIntegration(t *testing.T, cfg Config) *Integration {
	t.Helper()
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func testSigningKey() ed25519.PrivateKey {
	return ed25519.NewKeyFromSeed([]byte(strings.Repeat("a", ed25519.SeedSize)))
}

func keyResponse(t *testing.T, ids ...string) string {
	t.Helper()
	keys := make([]jose.JSONWebKey, 0, len(ids))
	for _, id := range ids {
		keys = append(keys, jose.JSONWebKey{Key: testSigningKey().Public(), KeyID: id, Algorithm: "EdDSA", Use: "sig"})
	}
	body, err := json.Marshal(map[string]any{"keys": keys})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestRetryConfiguration(t *testing.T) {
	a := newTestIntegration(t, testConfig())
	want := RetryConfig{MaxRetries: 3, InitialBackoff: 100 * time.Millisecond, MaxBackoff: time.Second, AttemptTimeout: time.Second, TotalTimeout: 5 * time.Second}
	if a.retry != want {
		t.Fatalf("defaults = %+v, want %+v", a.retry, want)
	}
	for _, tc := range []struct {
		name string
		r    RetryConfig
	}{
		{"negative count", RetryConfig{MaxRetries: -2}},
		{"negative initial", RetryConfig{InitialBackoff: -1}},
		{"negative maximum", RetryConfig{MaxBackoff: -1}},
		{"negative attempt", RetryConfig{AttemptTimeout: -1}},
		{"negative total", RetryConfig{TotalTimeout: -1}},
		{"inverted backoff", RetryConfig{InitialBackoff: 2 * time.Second, MaxBackoff: time.Second}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig()
			cfg.Retry = tc.r
			if _, err := New(cfg); err == nil {
				t.Fatal("accepted invalid configuration")
			}
		})
	}
}

func TestFetchRetryOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		failures int
		status   int
		body     string
		disabled bool
		attempts int
		success  bool
	}{
		{name: "first attempt", attempts: 1, success: true},
		{name: "success after retries", failures: 3, status: 503, attempts: 4, success: true},
		{name: "four attempts only", failures: 99, status: 502, attempts: 4},
		{name: "429", failures: 1, status: 429, attempts: 2, success: true},
		{name: "500", failures: 1, status: 500, attempts: 2, success: true},
		{name: "504", failures: 1, status: 504, attempts: 2, success: true},
		{name: "unauthorized", failures: 99, status: 401, attempts: 1},
		{name: "forbidden", failures: 99, status: 403, attempts: 1},
		{name: "bad request", failures: 99, status: 400, attempts: 1},
		{name: "not found", failures: 99, status: 404, attempts: 1},
		{name: "permanent server error", failures: 99, status: 501, attempts: 1},
		{name: "redirect", failures: 99, status: 302, attempts: 1},
		{name: "invalid JSON", failures: 99, status: 200, body: "{", attempts: 1},
		{name: "null response", failures: 99, status: 200, body: "null", attempts: 1},
		{name: "missing keys", failures: 99, status: 200, body: "{}", attempts: 1},
		{name: "empty keys", failures: 99, status: 200, body: `{"keys":[]}`, attempts: 1},
		{name: "invalid key", failures: 99, status: 200, body: `{"keys":[{"kid":"key-1"}]}`, attempts: 1},
		{name: "null key", failures: 99, status: 200, body: `{"keys":[null]}`, attempts: 1},
		{name: "disabled", failures: 99, status: 503, disabled: true, attempts: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				calls := 0
				var starts []time.Time
				var events []KeyFetchEvent
				cfg := testConfig()
				if tc.disabled {
					cfg.Retry.MaxRetries = -1
				}
				cfg.OnKeyFetch = func(_ context.Context, event KeyFetchEvent) { events = append(events, event) }
				cfg.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					calls++
					starts = append(starts, time.Now())
					if r.URL.String() != "https://api.clerk.com/v1/jwks" || r.Header.Get("Authorization") != "Bearer "+cfg.SecretKey {
						t.Error("SDK request changed")
					}
					if calls <= tc.failures {
						return response(tc.status, tc.body), nil
					}
					return response(200, keyResponse(t, "key-1")), nil
				})}
				a := newTestIntegration(t, cfg)
				key, err := a.key(context.Background(), "key-1")
				if calls != tc.attempts || (err == nil) != tc.success || (key != nil) != tc.success {
					t.Fatalf("calls=%d key=%v error=%v", calls, key != nil, err)
				}
				if len(events) != calls+1 || !events[calls].Final || events[calls].Attempt != calls || events[calls].Success != tc.success {
					t.Fatalf("diagnostics = %+v", events)
				}
				for i := 1; i < len(starts); i++ {
					base := (100 * time.Millisecond) << (i - 1)
					if delay := starts[i].Sub(starts[i-1]); delay < base*8/10 || delay > base*12/10 {
						t.Errorf("retry %d delay = %s", i, delay)
					}
				}
				// A subsequent request is subject to the existing cooldown, not
				// another sequence, even after exhaustion or an unknown key ID.
				_, _ = a.key(context.Background(), "unknown")
				if calls != tc.attempts {
					t.Fatal("cooldown did not bound unknown-key fetches")
				}
			})
		})
	}
}

func TestTransportFailures(t *testing.T) {
	for _, tc := range []struct {
		name  string
		err   error
		retry bool
	}{
		{"connection refused", &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, true},
		{"connection reset", syscall.ECONNRESET, true},
		{"unexpected EOF", io.ErrUnexpectedEOF, true},
		{"timeout", context.DeadlineExceeded, true},
		{"temporary DNS", &net.DNSError{IsTemporary: true}, true},
		{"missing DNS name", &net.DNSError{IsNotFound: true}, false},
		{"certificate", x509.UnknownAuthorityError{}, false},
		{"configuration", errors.New("invalid transport configuration"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				calls := 0
				cfg := testConfig()
				cfg.HTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					calls++
					if calls == 1 {
						return nil, tc.err
					}
					return response(200, keyResponse(t, "key-1")), nil
				})}
				key, err := newTestIntegration(t, cfg).key(context.Background(), "key-1")
				if (key != nil) != tc.retry || (err == nil) != tc.retry || calls != map[bool]int{true: 2, false: 1}[tc.retry] {
					t.Fatalf("calls=%d key=%v error=%v", calls, key != nil, err)
				}
			})
		})
	}
}

func TestRetryAfter(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		value  func(time.Time) string
		wait   time.Duration
		calls  int
	}{
		{"seconds", 429, func(time.Time) string { return "2" }, 2 * time.Second, 2},
		{"HTTP date", 503, func(now time.Time) string { return now.Add(2 * time.Second).UTC().Format(http.TimeFormat) }, 2 * time.Second, 2},
		{"exceeds budget", 429, func(time.Time) string { return "6" }, 0, 1},
		{"huge seconds", 503, func(time.Time) string { return "9999999999999999999999999999" }, 0, 1},
		{"ignored on 500", 500, func(time.Time) string { return "60" }, -1, 2},
		{"invalid header", 503, func(time.Time) string { return "invalid" }, -1, 2},
		{"negative header", 429, func(time.Time) string { return "-1" }, -1, 2},
		{"past date", 503, func(now time.Time) string { return now.Add(-time.Hour).UTC().Format(http.TimeFormat) }, -1, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				calls := 0
				start := time.Now()
				cfg := testConfig()
				cfg.HTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					calls++
					if calls > 1 {
						return response(200, keyResponse(t, "key-1")), nil
					}
					r := response(tc.status, "do not read this body")
					r.Header.Set("Retry-After", tc.value(time.Now()))
					return r, nil
				})}
				_, err := newTestIntegration(t, cfg).key(context.Background(), "key-1")
				if calls != tc.calls || (err == nil) != (tc.calls == 2) {
					t.Fatalf("calls=%d error=%v", calls, err)
				}
				elapsed := time.Since(start)
				if tc.wait >= 0 && elapsed != tc.wait {
					t.Fatalf("elapsed %s, want %s", elapsed, tc.wait)
				}
				if tc.wait < 0 && (elapsed < 80*time.Millisecond || elapsed > 120*time.Millisecond) {
					t.Fatalf("fallback backoff = %s", elapsed)
				}
			})
		})
	}
}

func TestFetchCancellationAndDeadlines(t *testing.T) {
	for _, tc := range []struct {
		name       string
		fetchWait  bool
		cancelAt   time.Duration
		deadlineAt time.Duration
		retry      RetryConfig
		calls      int
		elapsed    time.Duration
	}{
		{name: "cancel in fetch", fetchWait: true, cancelAt: 50 * time.Millisecond, calls: 1, elapsed: 50 * time.Millisecond},
		{name: "cancel in backoff", cancelAt: 50 * time.Millisecond, calls: 1, elapsed: 50 * time.Millisecond},
		{name: "earlier caller deadline", fetchWait: true, deadlineAt: 50 * time.Millisecond, calls: 1, elapsed: 50 * time.Millisecond},
		{name: "default attempt deadlines", fetchWait: true, calls: 4},
		{name: "total deadline", fetchWait: true, retry: RetryConfig{MaxRetries: 50, TotalTimeout: 1500 * time.Millisecond}, calls: 2, elapsed: 1500 * time.Millisecond},
		{name: "attempt exceeds total", fetchWait: true, retry: RetryConfig{AttemptTimeout: 10 * time.Second}, calls: 1, elapsed: 5 * time.Second},
		{name: "budget shorter than backoff", retry: RetryConfig{TotalTimeout: 50 * time.Millisecond}, calls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx := context.Background()
				if tc.deadlineAt != 0 {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, tc.deadlineAt)
					defer cancel()
				}
				ctx, cancel := context.WithCancel(ctx)
				defer cancel()
				if tc.cancelAt != 0 {
					go func() { time.Sleep(tc.cancelAt); cancel() }()
				}
				cfg := testConfig()
				cfg.Retry = tc.retry
				calls := 0
				var attemptDurations []time.Duration
				cfg.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					calls++
					if tc.fetchWait {
						start := time.Now()
						<-r.Context().Done()
						attemptDurations = append(attemptDurations, time.Since(start))
						return nil, r.Context().Err()
					}
					return response(503, ""), nil
				})}
				start := time.Now()
				key, err := newTestIntegration(t, cfg).key(ctx, "key-1")
				if key != nil || err == nil || calls != tc.calls {
					t.Fatalf("calls=%d key=%v error=%v", calls, key != nil, err)
				}
				if tc.elapsed != 0 && time.Since(start) != tc.elapsed {
					t.Fatalf("elapsed=%s, want %s", time.Since(start), tc.elapsed)
				}
				if tc.name == "default attempt deadlines" {
					for _, d := range attemptDurations {
						if d != time.Second {
							t.Errorf("attempt duration %s", d)
						}
					}
					if elapsed := time.Since(start); elapsed < 4560*time.Millisecond || elapsed > 4840*time.Millisecond {
						t.Fatalf("default sequence duration = %s", elapsed)
					}
				}
			})
		})
	}
}

type closeTrackingBody struct{ closed bool }

func (b *closeTrackingBody) Read([]byte) (int, error) { panic("failed response body must not be read") }
func (b *closeTrackingBody) Close() error             { b.closed = true; return nil }

func TestFailedBodyClosedBeforeRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		body := &closeTrackingBody{}
		calls := 0
		cfg := testConfig()
		cfg.HTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			if calls == 1 {
				r := response(503, "")
				r.Body = body
				return r, nil
			}
			if !body.closed {
				t.Fatal("previous body not closed")
			}
			return response(200, keyResponse(t, "key-1")), nil
		})}
		if _, err := newTestIntegration(t, cfg).key(context.Background(), "key-1"); err != nil {
			t.Fatal(err)
		}
	})
}

func TestConcurrentRefreshAndFreshCache(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(fmt.Sprintf("refresh failure=%v", failure), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cfg := testConfig()
				calls := 0
				cfg.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					calls++
					if calls == 1 {
						return response(200, keyResponse(t, "old")), nil
					}
					// Let all other callers reach the in-flight sequence.
					time.Sleep(10 * time.Millisecond)
					if failure || calls < 4 {
						return response(503, ""), nil
					}
					return response(200, keyResponse(t, "old", "rotated")), nil
				})}
				a := newTestIntegration(t, cfg)
				if _, err := a.key(context.Background(), "old"); err != nil {
					t.Fatal(err)
				}
				time.Sleep(31 * time.Second) // expire cooldown, keep keys fresh
				var wg sync.WaitGroup
				for range 20 {
					wg.Go(func() {
						key, err := a.key(context.Background(), "rotated")
						if (err != nil) != failure || (key == nil) != failure {
							t.Errorf("key=%v err=%v", key != nil, err)
						}
					})
				}
				synctest.Wait()
				start := time.Now()
				if key, err := a.key(context.Background(), "old"); key == nil || err != nil || time.Since(start) != 0 {
					t.Fatal("fresh cache blocked during refresh")
				}
				// Canceling a waiter must not cancel the initiating request.
				ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
				defer cancel()
				if _, err := a.key(ctx, "rotated"); !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("waiter error = %v", err)
				}
				wg.Wait()
				wantCalls := 4
				if failure {
					wantCalls = 5
				}
				if calls != wantCalls {
					t.Fatalf("coalesced calls=%d, want %d", calls, wantCalls)
				}
				if key, err := a.key(context.Background(), "old"); key == nil || err != nil {
					t.Fatal("refresh lost fresh cached key")
				}
				_, _ = a.key(context.Background(), "unknown")
				if calls != wantCalls {
					t.Fatal("unknown key bypassed cooldown")
				}
				if failure {
					time.Sleep(time.Hour)
					if key, err := a.key(context.Background(), "old"); key != nil || err == nil {
						t.Fatal("accepted stale key after refresh failure")
					}
				}
			})
		})
	}
}

func signedToken(t *testing.T, claims map[string]any, wrongSignature bool) string {
	t.Helper()
	key := testSigningKey()
	if wrongSignature {
		key = ed25519.NewKeyFromSeed([]byte(strings.Repeat("b", ed25519.SeedSize)))
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.EdDSA, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", "key-1").WithType("JWT"))
	if err != nil {
		t.Fatal(err)
	}
	token, err := josejwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestMiddlewareVerificationAndDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name       string
		failures   int
		status     int
		invalid    bool
		badSig     bool
		badClaims  bool
		wantStatus int
		calls      int
	}{
		{name: "first attempt verifies", wantStatus: 200, calls: 1},
		{name: "retry verifies same request", failures: 3, status: 503, wantStatus: 200, calls: 4},
		{name: "exhaustion generic 503", failures: 99, status: 503, wantStatus: 503, calls: 4},
		{name: "forbidden generic 503", failures: 99, status: 403, wantStatus: 503, calls: 1},
		{name: "invalid token", invalid: true, wantStatus: 401},
		{name: "invalid signature", badSig: true, wantStatus: 401, calls: 1},
		{name: "invalid claims", badClaims: true, wantStatus: 401, calls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			type correlationKey struct{}
			cfg := testConfig()
			cfg.Retry = RetryConfig{InitialBackoff: time.Millisecond, MaxBackoff: 5 * time.Millisecond}
			calls := 0
			var events []KeyFetchEvent
			cfg.OnKeyFetch = func(ctx context.Context, event KeyFetchEvent) {
				if ctx.Value(correlationKey{}) != "request-42" {
					t.Error("request correlation lost")
				}
				events = append(events, event)
			}
			const sensitive = "secret-cookie-authorization-upstream-body"
			cfg.HTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				if calls <= tc.failures {
					return response(tc.status, sensitive+cfg.SecretKey), nil
				}
				return response(200, keyResponse(t, "key-1")), nil
			})}
			a := newTestIntegration(t, cfg)
			app := fiber.New()
			app.Use(func(c fiber.Ctx) error {
				c.SetContext(context.WithValue(c.Context(), correlationKey{}, "request-42"))
				return c.Next()
			}, a.Middleware(), a.RequireUser())
			app.Get("/", func(c fiber.Ctx) error {
				session, ok := SessionFromContext(c.Context())
				if !ok || session.UserID != "user-1" || session.SessionID != "session-1" {
					t.Error("verified session missing")
				}
				return c.SendString("verified")
			})
			claims := map[string]any{"iss": "https://clerk.example.com", "sub": "user-1", "sid": "session-1", "azp": "https://example.com", "exp": time.Now().Add(time.Hour).Unix()}
			if tc.badClaims {
				claims["azp"] = "https://attacker.example"
			}
			token := signedToken(t, claims, tc.badSig)
			if tc.invalid {
				token = "invalid-token"
			}
			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Cookie", "__session="+sensitive)
			res, err := app.Test(req, fiber.TestConfig{Timeout: 0})
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			body, err := io.ReadAll(res.Body)
			if err != nil {
				t.Fatal(err)
			}
			if res.StatusCode != tc.wantStatus || calls != tc.calls {
				t.Fatalf("status=%d calls=%d body=%s", res.StatusCode, calls, body)
			}
			if tc.wantStatus == 503 && string(body) != "Authentication temporarily unavailable" {
				t.Fatalf("client error changed: %s", body)
			}
			if tc.wantStatus == 401 && string(body) != `{"error":"authentication required"}` {
				t.Fatalf("client auth error changed: %s", body)
			}
			if calls > 0 {
				last := events[len(events)-1]
				if !last.Final || last.Attempt != calls || last.Success != (tc.failures < calls) || last.Duration < 0 {
					t.Fatalf("final event=%+v", last)
				}
			}
			diagnostics := fmt.Sprint(events)
			for _, secret := range []string{cfg.SecretKey, sensitive, token} {
				if strings.Contains(diagnostics, secret) || strings.Contains(string(body), secret) {
					t.Fatal("credential leaked")
				}
			}
		})
	}
}

// Successful headers must not let a stalled response body escape the attempt
// timeout. This exercises the pinned SDK's body reader, not just RoundTrip.
type stalledKeyBody struct{ ctx context.Context }

func (b stalledKeyBody) Read([]byte) (int, error) {
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}
func (stalledKeyBody) Close() error { return nil }

func TestSuccessfulResponseBodyTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := testConfig()
		calls := 0
		cfg.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			if calls == 1 {
				res := response(200, "")
				res.Body = stalledKeyBody{ctx: r.Context()}
				return res, nil
			}
			return response(200, keyResponse(t, "key-1")), nil
		})}
		start := time.Now()
		key, err := newTestIntegration(t, cfg).key(context.Background(), "key-1")
		if key == nil || err != nil || calls != 2 || time.Since(start) < time.Second {
			t.Fatalf("calls=%d key=%v err=%v elapsed=%s", calls, key != nil, err, time.Since(start))
		}
	})
}

func TestInitiatingRequestCancellationEndsSharedRefresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := testConfig()
		calls := 0
		cfg.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			<-r.Context().Done()
			return nil, r.Context().Err()
		})}
		a := newTestIntegration(t, cfg)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var wg sync.WaitGroup
		wg.Go(func() {
			if _, err := a.key(ctx, "key-1"); !errors.Is(err, context.Canceled) {
				t.Errorf("initiator error = %v", err)
			}
		})
		synctest.Wait()
		wg.Go(func() {
			if _, err := a.key(context.Background(), "key-1"); !errors.Is(err, context.Canceled) {
				t.Errorf("waiter error = %v", err)
			}
		})
		synctest.Wait()
		cancel()
		wg.Wait()
		if calls != 1 {
			t.Fatalf("calls=%d", calls)
		}
	})
}

func TestCustomRetryCountAndBackoffCap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := testConfig()
		cfg.Retry = RetryConfig{MaxRetries: 5, InitialBackoff: 10 * time.Millisecond, MaxBackoff: 20 * time.Millisecond}
		calls := 0
		var previous time.Time
		cfg.HTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			if !previous.IsZero() && time.Since(previous) > 20*time.Millisecond {
				t.Error("backoff exceeded cap")
			}
			previous = time.Now()
			calls++
			return response(503, ""), nil
		})}
		if _, err := newTestIntegration(t, cfg).key(context.Background(), "key-1"); err == nil || calls != 6 {
			t.Fatalf("calls=%d err=%v", calls, err)
		}
	})
}

func TestDiagnosticCategoriesAndRedaction(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		err      error
		body     string
		category string
	}{
		{name: "http", status: 403, body: "SECRET_RESPONSE", category: "http"},
		{name: "configuration", err: errors.New("SECRET_TRANSPORT"), category: "permanent"},
		{name: "malformed", status: 200, body: "SECRET_JSON", category: "invalid_response"},
		{name: "timeout", err: context.DeadlineExceeded, category: "timeout"},
		{name: "connection", err: syscall.ECONNRESET, category: "connection"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig()
			cfg.Retry.MaxRetries = -1
			var events []KeyFetchEvent
			cfg.OnKeyFetch = func(_ context.Context, event KeyFetchEvent) { events = append(events, event) }
			configured := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				if tc.err != nil {
					return nil, tc.err
				}
				return response(tc.status, tc.body), nil
			})}
			originalTransport := http.DefaultTransport
			cfg.HTTPClient = configured
			a := newTestIntegration(t, cfg)
			_, err := a.key(context.Background(), "key-1")
			if err == nil || len(events) != 2 {
				t.Fatalf("events=%+v err=%v", events, err)
			}
			for _, event := range events {
				if event.Category != tc.category || event.StatusCode != tc.status || event.Attempt != 1 || event.Success {
					t.Errorf("event=%+v", event)
				}
			}
			if strings.Contains(fmt.Sprint(events, err), "SECRET") || strings.Contains(fmt.Sprint(events, err), cfg.SecretKey) {
				t.Fatal("raw failure details leaked")
			}
			if configured.CheckRedirect != nil || http.DefaultTransport != originalTransport {
				t.Fatal("modified supplied client or global transport")
			}
		})
	}
}

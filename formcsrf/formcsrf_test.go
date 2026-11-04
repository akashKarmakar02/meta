package formcsrf

import (
	"context"
	"strings"
	"testing"
	"time"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	m, err := New([]byte("test-secret-0123456789abcdefghijkl"), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

func TestIssueVerifyRoundTrip(t *testing.T) {
	m := newTestManager(t)
	tok := m.Issue()
	if tok == "" {
		t.Fatal("Issue returned empty token")
	}
	if !m.Verify(tok, tok) {
		t.Fatal("Verify rejected its own token as a matching pair")
	}
}

func TestVerifyRejectsMismatchedPair(t *testing.T) {
	m := newTestManager(t)
	a, b := m.Issue(), m.Issue()
	if m.Verify(a, b) {
		t.Fatal("Verify accepted a mismatched form/cookie pair")
	}
	if m.Verify(a, "") || m.Verify("", a) {
		t.Fatal("Verify accepted an empty half")
	}
}

func TestVerifyRejectsGarbage(t *testing.T) {
	m := newTestManager(t)
	for _, tok := range []string{"", "garbage", "::::", "a.b.c.d"} {
		if m.Verify(tok, tok) {
			t.Fatalf("Verify(%q) accepted garbage", tok)
		}
	}
}

func TestVerifyRejectsTamperedToken(t *testing.T) {
	m := newTestManager(t)
	tok := m.Issue()
	// Flip the leading payload character; the HMAC no longer matches.
	i := strings.IndexByte(tok, '.')
	if i <= 0 {
		t.Fatalf("token has no payload/sig separator: %q", tok)
	}
	flip := "A"
	if tok[0] == 'A' {
		flip = "B"
	}
	tampered := flip + tok[1:]
	if tampered == tok {
		t.Fatal("tamper attempt did not change the token")
	}
	if m.Verify(tampered, tampered) {
		t.Fatal("Verify accepted a tampered payload")
	}
}

func TestVerifyRejectsWrongSecret(t *testing.T) {
	m1, err := New([]byte("secret-one-0123456789abcdefghijk"), nil)
	if err != nil {
		t.Fatal(err)
	}
	m2, err := New([]byte("secret-two-0123456789abcdefghijk"), nil)
	if err != nil {
		t.Fatal(err)
	}
	tok := m1.Issue()
	if m2.Verify(tok, tok) {
		t.Fatal("token signed with a different secret verified")
	}
}

func TestVerifyRejectsExpiredToken(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	now := base
	m, err := New([]byte("test-secret-0123456789abcdefghijkl"), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	tok := m.Issue()
	now = base.Add(2 * TokenTTL)
	if m.Verify(tok, tok) {
		t.Fatal("expired token verified")
	}
}

func TestVerifyRejectsFutureToken(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	now := base
	m, err := New([]byte("test-secret-0123456789abcdefghijkl"), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	tok := m.Issue()
	now = base.Add(-time.Hour)
	if m.Verify(tok, tok) {
		t.Fatal("future-dated token verified")
	}
}

func TestValidToken(t *testing.T) {
	m := newTestManager(t)
	tok := m.Issue()
	if !m.ValidToken(tok) {
		t.Fatal("ValidToken rejected a fresh token")
	}
	if m.ValidToken("garbage") {
		t.Fatal("ValidToken accepted garbage")
	}
	if m.ValidToken(m.Issue() + "x") {
		t.Fatal("ValidToken accepted an appended suffix")
	}
}

func TestHiddenFieldIncludesToken(t *testing.T) {
	m := newTestManager(t)
	tok := m.Issue()
	ctx := WithContext(context.Background(), tok)
	var sb strings.Builder
	if err := HiddenField().Render(ctx, &sb); err != nil {
		t.Fatalf("HiddenField render: %v", err)
	}
	html := sb.String()
	if !strings.Contains(html, `type="hidden"`) {
		t.Fatalf("missing hidden type: %s", html)
	}
	if !strings.Contains(html, `name="_csrf"`) {
		t.Fatalf("missing field name: %s", html)
	}
	if !strings.Contains(html, `value="`+tok+`"`) {
		t.Fatalf("missing token value: %s", html)
	}
}

func TestHiddenFieldWithoutTokenRendersComment(t *testing.T) {
	var sb strings.Builder
	if err := HiddenField().Render(context.Background(), &sb); err != nil {
		t.Fatalf("HiddenField render: %v", err)
	}
	html := sb.String()
	if strings.Contains(html, "<input") {
		t.Fatalf("input rendered for context without token: %s", html)
	}
	if !strings.Contains(html, "no token in context") {
		t.Fatalf("missing diagnostic comment: %s", html)
	}
}

func TestFromContext(t *testing.T) {
	_, ok := FromContext(context.Background())
	if ok {
		t.Fatal("empty context yielded a token")
	}
	ctx := WithContext(context.Background(), "tok-1")
	got, ok := FromContext(ctx)
	if !ok || got != "tok-1" {
		t.Fatalf("FromContext = %q, %v", got, ok)
	}
}

func TestNewRejectsShortSecret(t *testing.T) {
	if _, err := New([]byte("short"), nil); err == nil {
		t.Fatal("New accepted a short secret")
	}
}

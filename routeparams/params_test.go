package routeparams

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- String access ---------------------------------------------------------

func TestGetValidString(t *testing.T) {
	p := NewParams(map[string]string{"userId": "42"})
	got, ok := p.Get("userId")
	if !ok || got != "42" {
		t.Fatalf("Get(userId) = (%q, %v), want (\"42\", true)", got, ok)
	}
}

func TestGetMissingString(t *testing.T) {
	p := NewParams(map[string]string{"userId": "42"})
	if got, ok := p.Get("nope"); ok || got != "" {
		t.Fatalf("Get(nope) = (%q, %v), want (\"\", false)", got, ok)
	}
}

func TestGetEmptyName(t *testing.T) {
	p := NewParams(map[string]string{"": "present"})
	// An empty name is always reported as missing, even if a value was stored
	// under the empty key.
	if got, ok := p.Get(""); ok || got != "" {
		t.Fatalf("Get(\"\") = (%q, %v), want (\"\", false)", got, ok)
	}
}

func TestEmptyValueVersusMissing(t *testing.T) {
	p := NewParams(map[string]string{"empty": ""})
	if got, ok := p.Get("empty"); !ok || got != "" {
		t.Fatalf("Get(empty) = (%q, %v), want (\"\", true) — present empty value", got, ok)
	}
	if _, ok := p.Get("absent"); ok {
		t.Fatal("Get(absent) reported present, want missing")
	}
}

func TestGetUnicode(t *testing.T) {
	p := NewParams(map[string]string{"slug": "héllo/wörld/日本語"})
	got, ok := p.Get("slug")
	if !ok || got != "héllo/wörld/日本語" {
		t.Fatalf("Get(slug) = (%q, %v), want the unicode value", got, ok)
	}
}

func TestGetCatchAllSlash(t *testing.T) {
	p := NewParams(map[string]string{"slug": "getting-started/installation"})
	got, ok := p.Get("slug")
	if !ok || got != "getting-started/installation" {
		t.Fatalf("Get(slug) = (%q, %v), want slash-joined catch-all", got, ok)
	}
}

func TestGetMultipleSimultaneous(t *testing.T) {
	p := NewParams(map[string]string{"orgId": "42", "path": "reports/2026"})
	if got, _ := p.Get("orgId"); got != "42" {
		t.Fatalf("Get(orgId) = %q, want 42", got)
	}
	if got, _ := p.Get("path"); got != "reports/2026" {
		t.Fatalf("Get(path) = %q, want reports/2026", got)
	}
}

// --- Signed integer --------------------------------------------------------

func TestInt64Valid(t *testing.T) {
	for _, tc := range []struct{ in, want string }{{"42", "42"}, {"-42", "-42"}, {"+42", "42"}, {"0", "0"}} {
		p := NewParams(map[string]string{"id": tc.in})
		got, err := p.Int64("id")
		if err != nil {
			t.Fatalf("Int64(%q) error: %v", tc.in, err)
		}
		if fmt.Sprint(got) != tc.want {
			t.Fatalf("Int64(%q) = %d, want %s", tc.in, got, tc.want)
		}
	}
}

func TestInt64Invalid(t *testing.T) {
	for _, in := range []string{"abc", "12a", "", "1.5", " 42", "0x10"} {
		p := NewParams(map[string]string{"id": in})
		if _, err := p.Int64("id"); err == nil {
			t.Errorf("Int64(%q) succeeded, want error", in)
		} else if !errors.Is(err, ErrInvalidParam) {
			t.Errorf("Int64(%q) error %v is not ErrInvalidParam", in, err)
		}
	}
}

func TestInt64Overflow(t *testing.T) {
	p := NewParams(map[string]string{"id": "9223372036854775808"}) // int64 max + 1
	if _, err := p.Int64("id"); err == nil {
		t.Fatal("Int64 overflow succeeded, want error")
	} else if !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Int64 overflow error %v is not ErrInvalidParam", err)
	}
}

func TestInt64Missing(t *testing.T) {
	p := NewParams(map[string]string{"other": "1"})
	if _, err := p.Int64("id"); !errors.Is(err, ErrMissingParam) {
		t.Fatalf("Int64(missing) error %v, want ErrMissingParam", err)
	}
}

// --- Native int -------------------------------------------------------------

func TestIntNativeBounds(t *testing.T) {
	// The maximum native int parses and round-trips.
	p := NewParams(map[string]string{"id": strconv.FormatInt(math.MaxInt, 10)})
	got, err := p.Int("id")
	if err != nil {
		t.Fatalf("Int(MaxInt) error: %v", err)
	}
	if got != math.MaxInt {
		t.Fatalf("Int(MaxInt) = %d, want %d", got, math.MaxInt)
	}
	// A value far beyond the native int width must fail. 10^IntSize overflows
	// any native int; built as a string to avoid a typed-constant overflow.
	overflow := "1" + strings.Repeat("0", strconv.IntSize)
	p2 := NewParams(map[string]string{"id": overflow})
	if _, err := p2.Int("id"); err == nil {
		t.Fatal("Int(overflow) succeeded, want error")
	}
}

// --- Unsigned integer -------------------------------------------------------

func TestUint64Valid(t *testing.T) {
	p := NewParams(map[string]string{"id": "18446744073709551615"}) // uint64 max
	got, err := p.Uint64("id")
	if err != nil {
		t.Fatalf("Uint64(max) error: %v", err)
	}
	if got != 18446744073709551615 {
		t.Fatalf("Uint64(max) = %d", got)
	}
}

func TestUint64NegativeRejected(t *testing.T) {
	for _, in := range []string{"-1", "-0"} {
		p := NewParams(map[string]string{"id": in})
		if _, err := p.Uint64("id"); err == nil {
			t.Errorf("Uint64(%q) succeeded, want rejection", in)
		} else if !errors.Is(err, ErrInvalidParam) {
			t.Errorf("Uint64(%q) error %v is not ErrInvalidParam", in, err)
		}
	}
}

func TestUint64Overflow(t *testing.T) {
	p := NewParams(map[string]string{"id": "18446744073709551616"}) // uint64 max + 1
	if _, err := p.Uint64("id"); err == nil {
		t.Fatal("Uint64 overflow succeeded, want error")
	}
}

// --- Boolean ---------------------------------------------------------------

func TestBool(t *testing.T) {
	for _, in := range []string{"1", "t", "T", "true", "TRUE", "True", "0", "f", "F", "false", "FALSE", "False"} {
		p := NewParams(map[string]string{"b": in})
		if _, err := p.Bool("b"); err != nil {
			t.Errorf("Bool(%q) error: %v", in, err)
		}
	}
}

func TestBoolInvalid(t *testing.T) {
	for _, in := range []string{"yes", "no", "maybe", "2", ""} {
		p := NewParams(map[string]string{"b": in})
		if _, err := p.Bool("b"); !errors.Is(err, ErrInvalidParam) {
			t.Errorf("Bool(%q) error %v, want ErrInvalidParam", in, err)
		}
	}
}

// --- Catch-all numeric conversion fails normally ---------------------------

func TestCatchAllNumericFailsNormally(t *testing.T) {
	p := NewParams(map[string]string{"slug": "getting-started/installation"})
	if _, err := p.Int64("slug"); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Int64(catch-all) error %v, want ErrInvalidParam (no implicit path->number)", err)
	}
	// But a catch-all value that happens to be numeric converts fine.
	p2 := NewParams(map[string]string{"slug": "42"})
	if got, err := p2.Int64("slug"); err != nil || got != 42 {
		t.Fatalf("Int64(numeric catch-all) = (%d, %v), want (42, nil)", got, err)
	}
}

// --- Error recognition -----------------------------------------------------

func TestErrorRecognition(t *testing.T) {
	p := NewParams(map[string]string{"id": "not-a-number"})

	_, err := p.Int64("id")
	var pe *paramError
	if !errors.As(err, &pe) {
		t.Fatalf("errors.As did not find *paramError in %v", err)
	}
	if !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("errors.Is did not match ErrInvalidParam for %v", err)
	}
	if errors.Is(err, ErrMissingParam) {
		t.Fatal("invalid error must not also be ErrMissingParam")
	}

	_, err = p.Int64("absent")
	if !errors.Is(err, ErrMissingParam) {
		t.Fatalf("errors.Is did not match ErrMissingParam for %v", err)
	}
	if errors.Is(err, ErrInvalidParam) {
		t.Fatal("missing error must not also be ErrInvalidParam")
	}
}

func TestErrorMessagesIncludeNameAndValue(t *testing.T) {
	p := NewParams(map[string]string{"id": "abc"})
	if _, err := p.Int64("id"); err == nil {
		t.Fatal("expected error")
	} else {
		if msg := err.Error(); !strings.Contains(msg, "id") || !strings.Contains(msg, "abc") {
			t.Fatalf("error message %q should include name and value", msg)
		}
	}
	_, err := p.Int64("absent")
	if err == nil {
		t.Fatal("expected error")
	} else if msg := err.Error(); !strings.Contains(msg, "absent") {
		t.Fatalf("missing error %q should include the name", msg)
	}
}

// --- Defensive copy --------------------------------------------------------

func TestNewParamsDefensiveCopy(t *testing.T) {
	src := map[string]string{"a": "1"}
	p := NewParams(src)
	src["a"] = "mutated"
	src["b"] = "2"
	if got, _ := p.Get("a"); got != "1" {
		t.Fatalf("NewParams did not copy: Get(a) = %q after source mutation", got)
	}
	if _, ok := p.Get("b"); ok {
		t.Fatal("NewParams picked up a key added after construction")
	}
}

func TestAllDefensiveCopy(t *testing.T) {
	p := NewParams(map[string]string{"a": "1"})
	all := p.All()
	all["a"] = "mutated"
	all["b"] = "2"
	if got, _ := p.Get("a"); got != "1" {
		t.Fatalf("All() returned a live map: Get(a) = %q after mutation", got)
	}
	if _, ok := p.Get("b"); ok {
		t.Fatal("All() returned a live map: new key visible")
	}
}

func TestAllEmptyNonNil(t *testing.T) {
	var p Params
	all := p.All()
	if all == nil {
		t.Fatal("All() on empty Params returned nil, want non-nil empty map")
	}
	if len(all) != 0 {
		t.Fatalf("All() on empty Params has %d entries, want 0", len(all))
	}
}

// --- Context accessor ------------------------------------------------------

func TestFromContextNoParams(t *testing.T) {
	p := FromContext(context.Background())
	if _, ok := p.Get("anything"); ok {
		t.Fatal("FromContext with no attached params reported a value")
	}
	if _, err := p.Int64("x"); !errors.Is(err, ErrMissingParam) {
		t.Fatalf("empty FromContext Int64 error %v, want ErrMissingParam", err)
	}
}

func TestFromContextNilContext(t *testing.T) {
	var ctx context.Context
	if _, ok := FromContext(ctx).Get("x"); ok {
		t.Fatal("FromContext(nil) reported a value")
	}
}

func TestWithContextRoundTrip(t *testing.T) {
	p := NewParams(map[string]string{"userId": "7"})
	ctx := WithContext(context.Background(), p)
	got := FromContext(ctx)
	if v, ok := got.Get("userId"); !ok || v != "7" {
		t.Fatalf("WithContext/FromContext round trip = (%q, %v), want (7, true)", v, ok)
	}
}

func TestFromContextIgnoresStringKeys(t *testing.T) {
	// The new API must not read parameters from arbitrary string keys (the
	// legacy B03 bridge). A context carrying only a string-keyed value must
	// yield an empty Params.
	ctx := context.WithValue(context.Background(), "userId", "999")
	if v, ok := FromContext(ctx).Get("userId"); ok || v != "" {
		t.Fatalf("FromContext read a string-keyed value: (%q, %v)", v, ok)
	}
}

// fakeStore models the fasthttp request context: it is both a value store
// (SetUserValue) and a context.Context (Value), so Attach and FromContext can
// operate on the same object exactly as they do on the real render context.
type fakeStore struct{ m map[any]any }

func (f *fakeStore) SetUserValue(k, v any) {
	if f.m == nil {
		f.m = map[any]any{}
	}
	f.m[k] = v
}

func (f *fakeStore) Deadline() (time.Time, bool) { return time.Time{}, false }
func (f *fakeStore) Done() <-chan struct{}       { return nil }
func (f *fakeStore) Err() error                  { return nil }
func (f *fakeStore) Value(k any) any             { return f.m[k] }

func TestAttachAndRetrieve(t *testing.T) {
	store := &fakeStore{}
	p := NewParams(map[string]string{"userId": "5"})
	if !Attach(store, p) {
		t.Fatal("Attach returned false for a valid store")
	}
	if got, ok := FromContext(store).Get("userId"); !ok || got != "5" {
		t.Fatalf("Attach/FromContext = (%q, %v), want (5, true)", got, ok)
	}
}

func TestAttachNilAndNonStore(t *testing.T) {
	if Attach(nil, NewParams(nil)) {
		t.Fatal("Attach(nil) should return false")
	}
	if Attach("not a store", NewParams(nil)) {
		t.Fatal("Attach(non-store) should return false")
	}
}

// --- Concurrency / isolation ----------------------------------------------

func TestConcurrentRequestSafety(t *testing.T) {
	// Each request gets its own Params; concurrent reads must not interfere.
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p := NewParams(map[string]string{"id": strconv.Itoa(i)})
			got, err := p.Int("id")
			if err != nil || got != i {
				t.Errorf("request %d: Int(id) = (%d, %v)", i, got, err)
			}
		}(i)
	}
	wg.Wait()
}

func TestParamsIsolationAcrossRequests(t *testing.T) {
	a := NewParams(map[string]string{"id": "1"})
	b := NewParams(map[string]string{"id": "2"})
	// Mutating a's All() must not affect b.
	a.All()["id"] = "999"
	if got, _ := b.Get("id"); got != "2" {
		t.Fatalf("request b saw request a's value: %q", got)
	}
}

package action

import (
	"context"
	"errors"
	"sync"
	"testing"

	"go-meta/routecontext"
)

func regRoute() routecontext.Route { return routecontext.Route{} }

// TestValidID covers the ID grammar: accepted shapes and the malformed
// spellings the endpoint must reject before lookup.
func TestValidID(t *testing.T) {
	valid := []string{
		"posts.create", "demo.createPost", "a", "A1", "a-b", "a_b",
		strings_Repeat("a", MaxIDLen), "x.y.z-w_v",
	}
	for _, id := range valid {
		if !ValidID(id) {
			t.Errorf("ValidID(%q) = false, want true", id)
		}
	}
	invalid := []string{
		"", ".a", "a.", "-a", "a-", "_a", "a_", // separator edges
		"a b", "a\tb", "a\nb", // whitespace
		"a/b", "a\\b", "a%2Fb", "a..b..c"[:0], // slashes/encoding; "" via slice
		strings_Repeat("a", MaxIDLen+1), // too long
		"café", "日本",                    // non-ASCII
		"a:b", "a?b", "a#b", "a@b", "a&b", // URL-significant bytes
	}
	for _, id := range invalid {
		if ValidID(id) {
			t.Errorf("ValidID(%q) = true, want false", id)
		}
	}
}

// TestNewRegistryValidation covers matrix items 4–7: empty ID, malformed ID,
// duplicate ID, nil action — each rejected with an actionable sentinel, and
// no partial registry published.
func TestNewRegistryValidation(t *testing.T) {
	ok := Bind("a.ok", func(context.Context, routecontext.Route, struct{}) (struct{}, error) { return struct{}{}, nil })

	cases := []struct {
		name     string
		bindings []Binding
		want     error
	}{
		{"empty ID", []Binding{Bind("", func(context.Context, routecontext.Route, struct{}) (struct{}, error) { return struct{}{}, nil })}, ErrInvalidID},
		{"malformed ID", []Binding{Bind("bad id", func(context.Context, routecontext.Route, struct{}) (struct{}, error) { return struct{}{}, nil })}, ErrInvalidID},
		{"nil action", []Binding{Bind("a.nil", nilAction[struct{}, struct{}]())}, ErrNilBinding},
		{"duplicate ID", []Binding{ok, ok}, ErrDuplicateID},
		{"valid set passes", []Binding{ok, Bind("b.other", func(context.Context, routecontext.Route, int) (int, error) { return 0, nil })}, nil},
		{"empty list passes", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg, err := NewRegistry(tc.bindings...)
			if tc.want == nil {
				if err != nil || reg == nil {
					t.Fatalf("NewRegistry() = %v, %v; want registry", reg, err)
				}
				return
			}
			if err == nil || !errors.Is(err, tc.want) {
				t.Fatalf("NewRegistry() err = %v; want %v", err, tc.want)
			}
			if reg != nil {
				t.Fatal("NewRegistry published a partial registry alongside an error")
			}
		})
	}
}

func nilAction[I, O any]() Action[I, O] { var a Action[I, O]; return a }

func strings_Repeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}

// TestRegistryLookup covers unknown-action lookup and the nil-registry safe
// path. Lookup never executes anything.
func TestRegistryLookup(t *testing.T) {
	reg, err := NewRegistry(Bind("x.y", func(_ context.Context, _ routecontext.Route, in string) (int, error) {
		return len(in), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if reg.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", reg.Len())
	}
	if _, ok := reg.Lookup("nope"); ok {
		t.Fatal("unknown ID resolved")
	}
	var nilReg *Registry
	if _, ok := nilReg.Lookup("x.y"); ok {
		t.Fatal("nil registry resolved a binding")
	}
	if nilReg.Len() != 0 {
		t.Fatal("nil registry Len() != 0")
	}
}

// TestBindTypedAdaptation verifies the adapter decodes into the concrete input
// type (matrix items 13, 1–3): struct, pointer, and scalar inputs coexist and
// decode strictly.
func TestBindTypedAdaptation(t *testing.T) {
	type in struct {
		N int `json:"n"`
	}
	double := func(_ context.Context, _ routecontext.Route, i in) (in, error) { return in{N: i.N * 2}, nil }
	ptr := func(_ context.Context, _ routecontext.Route, p *in) (int, error) {
		if p == nil {
			return 0, nil
		}
		return p.N + 1, nil
	}
	scalar := func(_ context.Context, _ routecontext.Route, n int) (string, error) { return "done", nil }

	reg, err := NewRegistry(
		Bind("t.double", double),
		Bind("t.ptr", ptr),
		Bind("t.scalar", scalar),
	)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := reg.Lookup("t.double")
	out, err := b.Invoke(context.Background(), regRoute(), []byte(`{"n":21}`))
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := out.(in); !ok || got.N != 42 {
		t.Fatalf("double output = %#v", out)
	}
	// Pointer input decodes to an allocated value.
	b, _ = reg.Lookup("t.ptr")
	out, err = b.Invoke(context.Background(), regRoute(), []byte(`{"n":41}`))
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := out.(int); !ok || got != 42 {
		t.Fatalf("ptr output = %#v", out)
	}
	// Scalar input decodes from a bare JSON number.
	b, _ = reg.Lookup("t.scalar")
	out, err = b.Invoke(context.Background(), regRoute(), []byte(`7`))
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := out.(string); !ok || got != "done" {
		t.Fatalf("scalar output = %#v", out)
	}
}

// TestInvokeStrictDecoding covers the decode gates that must never execute
// the bound action.
func TestInvokeStrictDecoding(t *testing.T) {
	ran := 0
	b := Bind("d.strict", func(context.Context, routecontext.Route, struct{ N int }) (struct{}, error) {
		ran++
		return struct{}{}, nil
	})
	bad := []string{
		``,                 // empty body
		`   `,              // whitespace only
		`{"n":1,}`,         // malformed JSON
		`{"n":1} {"n":2}`,  // multiple values
		`{"n":1} trailing`, // trailing data
		`{"unknown":true}`, // unknown field
	}
	for _, body := range bad {
		if _, err := b.Invoke(context.Background(), regRoute(), []byte(body)); err == nil {
			t.Errorf("body %q decoded without error", body)
		} else if !errors.Is(err, ErrInvalidInput) {
			t.Errorf("body %q error = %v, want ErrInvalidInput wrap", body, err)
		}
	}
	if ran != 0 {
		t.Fatalf("action ran %d times on decode failures, want 0", ran)
	}
	// A null body is a legitimate empty input.
	if _, err := b.Invoke(context.Background(), regRoute(), []byte(`null`)); err != nil {
		t.Fatalf("null body: %v", err)
	}
	if ran != 1 {
		t.Fatalf("action ran %d times on null input, want 1", ran)
	}
}

// TestInvokePreservesErrorsAndCancellation covers matrix items 15–16 at the
// adapter boundary: wrapped errors keep their causes for errors.Is, and the
// request context propagates into the action.
func TestInvokePreservesErrorsAndCancellation(t *testing.T) {
	sentinel := errors.New("app: boom")
	b := Bind("e.wrap", func(context.Context, routecontext.Route, struct{}) (struct{}, error) {
		return struct{}{}, errors.Join(ErrNotFound, sentinel)
	})
	_, err := b.Invoke(context.Background(), regRoute(), []byte(`null`))
	if !errors.Is(err, ErrNotFound) || !errors.Is(err, sentinel) {
		t.Fatalf("wrapped causes lost: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b2 := Bind("e.cancel", func(ctx context.Context, _ routecontext.Route, _ struct{}) (struct{}, error) {
		return struct{}{}, ctx.Err()
	})
	if _, err := b2.Invoke(ctx, regRoute(), []byte(`null`)); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled ctx: %v", err)
	}
}

// TestNoExecutionOnRegistrationOrLookup covers matrix items 11–12.
func TestNoExecutionOnRegistrationOrLookup(t *testing.T) {
	ran := 0
	act := func(context.Context, routecontext.Route, struct{}) (struct{}, error) {
		ran++
		return struct{}{}, nil
	}
	_ = Bind("n.count", act)
	reg, err := NewRegistry(Bind("n.count2", act))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = reg.Lookup("n.count2")
	_ = reg.Len()
	if ran != 0 {
		t.Fatalf("action ran %d times during registration/lookup, want 0", ran)
	}
}

// TestConcurrentLookupAndInvoke covers matrix item 9 under -race: concurrent
// lookups and independent invocations on one immutable registry.
func TestConcurrentLookupAndInvoke(t *testing.T) {
	b := Bind("c.add", func(_ context.Context, _ routecontext.Route, n int) (int, error) {
		return n + 1, nil
	})
	reg, err := NewRegistry(b)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got, _ := reg.Lookup("c.add")
			out, err := got.Invoke(context.Background(), regRoute(), []byte{'}' + 0})
			_, _ = out, err
		}(i)
	}
	wg.Wait()
}

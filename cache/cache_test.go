package cache

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// fakeStore is the minimal Store double used to pin the interface's contract
// assumptions. It is NOT a storage implementation: no TTL, no capacity, no
// eviction — those are E02. It exists to make copy-in/copy-out, outcome
// distinctions, and context behavior executable instead of merely documented.
type fakeStore struct {
	m       map[Key][]byte
	closed  bool
	failAll error // when non-nil, every operation reports this store error
}

func newFakeStore() *fakeStore {
	return &fakeStore{m: make(map[Key][]byte)}
}

var _ Store = (*fakeStore)(nil)

func (f *fakeStore) Get(ctx context.Context, k Key) (Entry, bool, error) {
	if ctx == nil {
		return Entry{}, false, ErrNilContext
	}
	if err := k.Validate(); err != nil {
		return Entry{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return Entry{}, false, err
	}
	if f.closed {
		return Entry{}, false, ErrClosed
	}
	if f.failAll != nil {
		return Entry{}, false, f.failAll
	}
	v, ok := f.m[k]
	if !ok {
		return Entry{}, false, nil
	}
	out := make([]byte, len(v))
	copy(out, v)
	return Entry{Value: out}, true, nil
}

func (f *fakeStore) Set(ctx context.Context, k Key, e Entry, p Policy) error {
	if ctx == nil {
		return ErrNilContext
	}
	if err := k.Validate(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := p.Validate(); err != nil {
		return err
	}
	if f.closed {
		return ErrClosed
	}
	stored := make([]byte, len(e.Value))
	copy(stored, e.Value)
	f.m[k] = stored
	return nil
}

func (f *fakeStore) Delete(ctx context.Context, k Key) error {
	if ctx == nil {
		return ErrNilContext
	}
	if err := k.Validate(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if f.closed {
		return ErrClosed
	}
	delete(f.m, k)
	return nil
}

func TestKeyValidate(t *testing.T) {
	cases := []struct {
		name string
		key  Key
		ok   bool
	}{
		{"simple", "loader:profile:42", true},
		{"empty", "", false},
		{"unicode", "ns:ユーザー:42", true},
		{"invalid utf8", Key("ns:\xff\xfe"), false},
		{"leading space", " ns:x", false},
		{"trailing space", "ns:x ", false},
		{"tab inside component", "ns:a\tb", false},
		{"nul byte", Key("ns:a\x00b"), false},
		{"at max length", Key(strings.Repeat("a", MaxKeyLength)), true},
		{"one over max", Key(strings.Repeat("a", MaxKeyLength+1)), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.key.Validate()
			if tc.ok && err != nil {
				t.Fatalf("key should validate: %v", err)
			}
			if !tc.ok && !errors.Is(err, ErrInvalidKey) {
				t.Fatalf("want ErrInvalidKey, got %v", err)
			}
		})
	}
}

// TestKeyLengthBoundary pins MaxKeyLength exactly, so it cannot drift silently.
func TestKeyLengthBoundary(t *testing.T) {
	for _, n := range []int{0, 1, MaxKeyLength - 1, MaxKeyLength, MaxKeyLength + 1} {
		key := Key(strings.Repeat("a", n))
		err := key.Validate()
		wantErr := n == 0 || n > MaxKeyLength
		if wantErr != (err != nil) {
			t.Fatalf("length %d: wantErr=%v, got %v", n, wantErr, err)
		}
	}
}

// TestNewKeyDeterministicIdentity checks that identical inputs produce the
// identical key — the property E05 singleflight coalescing depends on.
func TestNewKeyDeterministicIdentity(t *testing.T) {
	a, err := NewKey("loader:user-profile", "42", "posts")
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewKey("loader:user-profile", "42", "posts")
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("identical inputs gave different keys: %q vs %q", a, b)
	}
	// Different components give different keys.
	c, err := NewKey("loader:user-profile", "42", "comments")
	if err != nil {
		t.Fatal(err)
	}
	if a == c {
		t.Fatal("different query component produced identical key")
	}
	d, err := NewKey("loader:user-profile", "99", "posts")
	if err != nil {
		t.Fatal(err)
	}
	if a == d {
		t.Fatal("different parameter component produced identical key")
	}
}

func TestNewKeyNamespace(t *testing.T) {
	if _, err := NewKey(""); err == nil || !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("empty namespace must fail with ErrInvalidKey, got %v", err)
	}
	if _, err := NewKey(strings.Repeat("n", maxNamespaceLength+1)); err == nil || !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("over-long namespace must fail with ErrInvalidKey, got %v", err)
	}
	if _, err := NewKey(" padded"); err == nil || !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("padded namespace must fail with ErrInvalidKey, got %v", err)
	}
	if _, err := NewKey("ns\xff"); err == nil || !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("invalid-UTF-8 namespace must fail with ErrInvalidKey, got %v", err)
	}
	// Namespace isolation: same components under different namespaces give
	// different keys, so a loader key and an image key never collide.
	a, _ := NewKey("loader:profile", "42")
	b, _ := NewKey("image:thumbnail", "42")
	if a == b {
		t.Fatal("different namespaces produced identical keys")
	}
}

// TestNewKeyCollisionResistance pins the property separators cannot give:
// distinct component sequences must produce distinct keys. This is the exact
// scenario (route-plus-query shapes like /users/42?tab=posts versus inputs
// containing separator text) that makes ad hoc "a:b:c" joining unsafe.
func TestNewKeyCollisionResistance(t *testing.T) {
	pairs := [][2][]string{
		{[]string{"a:b", "c"}, []string{"a", "b:c"}},
		{[]string{"", "a"}, []string{"a"}},           // empty vs absent component
		{[]string{"ab", "c"}, []string{"a", "bc"}},   // split-point ambiguity
		{[]string{"a:", ":a"}, []string{"a::", "a"}}, // embedded separators
		{[]string{"a", "", "b"}, []string{"a", "b"}}, // interior empty
		{[]string{"日", "本"}, []string{"日本"}},         // multi-byte join ambiguity
	}
	for i, pair := range pairs {
		x, err1 := NewKey("ns", pair[0]...)
		y, err2 := NewKey("ns", pair[1]...)
		if err1 != nil || err2 != nil {
			t.Fatalf("shape %d: %v, %v", i, err1, err2)
		}
		if x == y {
			t.Fatalf("shape %d collided: %q == %q (components %q vs %q)", i, x, y, pair[0], pair[1])
		}
	}
}

func TestNewKeyUnicodeAndEmptyComponents(t *testing.T) {
	// Multi-byte runes encode by UTF-8 byte length; round-trip determinism.
	a, err := NewKey("ns", "ユーザー", "42")
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewKey("ns", "ユーザー", "42")
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("identical unicode inputs must give identical keys")
	}
	// An empty component is valid and distinct from an absent one.
	withEmpty, err := NewKey("ns", "")
	if err != nil {
		t.Fatal(err)
	}
	without, err := NewKey("ns")
	if err != nil {
		t.Fatal(err)
	}
	if withEmpty == without {
		t.Fatal("empty component must be distinct from absent component")
	}
}

// TestStoreOutcomeCombinationTable pins hit/miss/error semantics through the
// contract double. These are interface semantics, not fake behavior.
func TestStoreOutcomeCombinationTable(t *testing.T) {
	ctx := context.Background()
	s := newFakeStore()
	key, _ := NewKey("ns", "k")

	// Miss is not an error.
	e, ok, err := s.Get(ctx, key)
	if ok || err != nil || e.Value != nil {
		t.Fatalf("expected clean miss, got entry=%v ok=%v err=%v", e, ok, err)
	}
	// Empty value is a hit: a cached empty list or JSON null is a hit.
	if err := s.Set(ctx, key, Entry{Value: nil}, Policy{TTL: time.Minute}); err != nil {
		t.Fatal(err)
	}
	e, ok, err = s.Get(ctx, key)
	if !ok || err != nil || len(e.Value) != 0 {
		t.Fatalf("nil value must be a hit with empty Value, got entry=%v ok=%v err=%v", e, ok, err)
	}
	// Store error is distinguishable from miss.
	s.failAll = errors.New("disk exploded")
	e, ok, err = s.Get(ctx, key)
	if ok || err == nil || !strings.Contains(err.Error(), "disk exploded") {
		t.Fatalf("store error must be reported distinctly, got entry=%v ok=%v err=%v", e, ok, err)
	}
	s.failAll = nil
}

func TestStoreMemoryOwnership(t *testing.T) {
	ctx := context.Background()
	s := newFakeStore()
	key, _ := NewKey("ns", "k")

	// Scenario 1: caller mutates after Set — store unaffected.
	original := []byte("hello")
	if err := s.Set(ctx, key, Entry{Value: original}, Policy{TTL: time.Minute}); err != nil {
		t.Fatal(err)
	}
	original[0] = 'X'
	e, ok, _ := s.Get(ctx, key)
	if !ok || string(e.Value) != "hello" {
		t.Fatalf("mutating caller slice after Set changed the store: %q", e.Value)
	}
	// Scenario 2: caller mutates returned bytes — future reads unaffected.
	e.Value[0] = 'Y'
	e2, ok, _ := s.Get(ctx, key)
	if !ok || string(e2.Value) != "hello" {
		t.Fatalf("mutating returned bytes changed future reads: %q", e2.Value)
	}
}

// TestStoreCopySemanticsThroughInterface executes the documented
// copy-in/copy-out requirement through the exported interface type, proving
// ordinary callers cannot mutate shared state without knowing the
// implementation.
func TestStoreCopySemanticsThroughInterface(t *testing.T) {
	var s Store = newFakeStore()
	ctx := context.Background()
	key, _ := NewKey("ns", "k")
	buf := []byte("v")
	if err := s.Set(ctx, key, Entry{Value: buf}, Policy{TTL: time.Minute}); err != nil {
		t.Fatal(err)
	}
	buf[0] = 'w'
	e, ok, err := s.Get(ctx, key)
	if err != nil || !ok || string(e.Value) != "v" {
		t.Fatalf("copy-in violated: %q ok=%v err=%v", e.Value, ok, err)
	}
	e.Value[0] = 'z'
	e2, ok, _ := s.Get(ctx, key)
	if !ok || string(e2.Value) != "v" {
		t.Fatalf("copy-out violated: %q", e2.Value)
	}
}

func TestStoreContextCancellation(t *testing.T) {
	s := newFakeStore()
	key, _ := NewKey("ns", "k")

	// Nil context is rejected, never silently Background.
	if _, _, err := s.Get(nil, key); !errors.Is(err, ErrNilContext) {
		t.Fatalf("Get(nil) must reject with ErrNilContext, got %v", err)
	}
	if err := s.Delete(nil, key); !errors.Is(err, ErrNilContext) {
		t.Fatalf("Delete(nil) must reject with ErrNilContext, got %v", err)
	}
	if err := s.Set(nil, key, Entry{Value: []byte("x")}, Policy{TTL: time.Minute}); !errors.Is(err, ErrNilContext) {
		t.Fatalf("Set(nil) must reject with ErrNilContext, got %v", err)
	}

	// Already-canceled context yields the context error and no side effects.
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok, err := s.Get(canceled, key); ok || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Get: ok=%v err=%v", ok, err)
	}
	if err := s.Set(canceled, key, Entry{Value: []byte("x")}, Policy{TTL: time.Minute}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Set: %v", err)
	}
	if err := s.Delete(canceled, key); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Delete: %v", err)
	}
	// The canceled Set stored nothing.
	if e, ok, err := s.Get(context.Background(), key); ok || e.Value != nil || err != nil {
		t.Fatalf("canceled Set left data behind: %v %v %v", e, ok, err)
	}

	// Deadline exceeded propagates distinctly from Canceled.
	expired, cancelDeadline := context.WithTimeout(context.Background(), 0)
	defer cancelDeadline()
	if _, _, err := s.Get(expired, key); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired Get: %v", err)
	}
}

func TestStoreDeleteSemantics(t *testing.T) {
	ctx := context.Background()
	s := newFakeStore()
	key, _ := NewKey("ns", "k")
	if err := s.Set(ctx, key, Entry{Value: []byte("v")}, Policy{TTL: time.Minute}); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, key); err != nil {
		t.Fatalf("delete present key: %v", err)
	}
	if _, ok, _ := s.Get(ctx, key); ok {
		t.Fatal("deleted key still readable")
	}
	// Deleting a missing key is a successful no-op: exact-key invalidation is
	// idempotent, so E04 can invalidate speculatively.
	if err := s.Delete(ctx, key); err != nil {
		t.Fatalf("delete missing key must be nil, got %v", err)
	}
	other, _ := NewKey("ns", "other")
	if err := s.Delete(ctx, other); err != nil {
		t.Fatalf("delete never-set key must be nil, got %v", err)
	}
}

func TestStoreInvalidInputs(t *testing.T) {
	ctx := context.Background()
	s := newFakeStore()
	key, _ := NewKey("ns", "k")

	// Invalid policy rejected at Set without storing.
	if err := s.Set(ctx, key, Entry{Value: []byte("v")}, Policy{}); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("zero policy must be rejected with ErrInvalidPolicy, got %v", err)
	}
	if err := s.Set(ctx, key, Entry{Value: []byte("v")}, Policy{TTL: -time.Second}); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("negative TTL must be rejected with ErrInvalidPolicy, got %v", err)
	}
	if _, ok, _ := s.Get(ctx, key); ok {
		t.Fatal("rejected Set stored an entry anyway")
	}
	// Invalid key rejected by the double per the interface contract.
	if _, _, err := s.Get(ctx, ""); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("empty key at Get must report ErrInvalidKey, got %v", err)
	}
}

func TestStoreIndependentInstances(t *testing.T) {
	ctx := context.Background()
	a, b := newFakeStore(), newFakeStore()
	key, _ := NewKey("ns", "k")
	if err := a.Set(ctx, key, Entry{Value: []byte("A")}, Policy{TTL: time.Minute}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := b.Get(ctx, key); ok {
		t.Fatal("independent stores share state")
	}
	if err := b.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := a.Get(ctx, key); !ok {
		t.Fatal("delete on one store removed from another")
	}
}

// TestErrorCategoryPreservation proves wrapped causes survive errors.As and
// wrapped categories survive errors.Is.
func TestErrorCategoryPreservation(t *testing.T) {
	wrapped := fmt.Errorf("set route /users: %w", ErrEntryTooLarge)
	if !errors.Is(wrapped, ErrEntryTooLarge) {
		t.Fatal("errors.Is failed on category")
	}
	unwrapped := fmt.Errorf("store %q: %w", "my store", ErrClosed)
	if !errors.Is(unwrapped, ErrClosed) {
		t.Fatal("errors.Is failed on closed category")
	}
	ordinary := errors.New("some driver failure")
	if errors.Is(ordinary, ErrInvalidKey) {
		t.Fatal("ordinary error matched a category")
	}
}

// TestPolicyValidationTable covers zero/negative/oversize/non-finite TTLs and
// the valid range boundaries.
func TestPolicyValidationTable(t *testing.T) {
	cases := []struct {
		name  string
		ttl   time.Duration
		valid bool
	}{
		{"zero ttl", 0, false},
		{"negative ttl", -time.Second, false},
		{"one nanosecond", time.Nanosecond, true},
		{"one second", time.Second, true},
		{"max ttl", MaxTTL, true},
		{"above max", MaxTTL + time.Second, false},
		{"non-finite", time.Duration(1 << 62), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Policy{TTL: tc.ttl}.Validate()
			if tc.valid && err != nil {
				t.Fatalf("valid policy rejected: %v", err)
			}
			if !tc.valid && !errors.Is(err, ErrInvalidPolicy) {
				t.Fatalf("invalid policy must wrap ErrInvalidPolicy, got %v", err)
			}
		})
	}
}

// TestNewKeyAssembledLengthBound checks that NewKey validates the assembled
// result rather than only its parts.
func TestNewKeyAssembledLengthBound(t *testing.T) {
	huge := strings.Repeat("a", MaxKeyLength)
	if _, err := NewKey("ns", huge); err == nil || !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("assembled over-length key must fail with ErrInvalidKey, got %v", err)
	}
	// The same total content split across two components still exceeds.
	if _, err := NewKey("ns", huge[:1000], huge[1000:]); err == nil {
		t.Fatal("split components must still honor the assembled bound")
	}
}

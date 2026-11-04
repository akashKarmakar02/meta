// Package cache defines go-meta's public caching contract: cache keys,
// entries, policies, error categories, and the Store interface.
//
// The package provides the Store contract and the bounded Memory implementation.
// Loader integration, invalidation orchestration, and singleflight coalescing
// are explicitly opt-in through package loader. Applications that do not
// configure a cached binding retain the pre-cache behavior.
//
// # Why bytes
//
// Entry holds Value []byte rather than an arbitrary Go value or a generic
// type parameter. Three reasons, in order of weight:
//
//  1. The framework already crosses a byte boundary: CSR loader results are
//     delivered to the browser as encoding/json output ([]byte), so a cache
//     keyed on bytes reuses the existing serialization rather than adding a
//     second one. SSR typed values will be adapted to and from bytes by the
//     E03 loader integration; the cache contract stays type-agnostic.
//  2. Ownership is unambiguous. Byte slices can be copied on the way in and
//     on the way out, so neither the caller nor a later reader can mutate a
//     stored entry accidentally. An "any"-typed store cannot promise that
//     (maps, slices, and pointers alias), and a comment claiming immutability
//     does not make it true.
//  3. Size accounting is exact. A bounded store (E02) counts len(Value) plus
//     the key length; no reflection or guesswork.
//
// A typed convenience wrapper, if one proves necessary, is added by E03 on top
// of this contract — not instead of it.
//
// # What may be cached
//
// Only successful results are candidates for caching. Callers must NOT cache
// loader failures (ErrNotFound, ErrUnauthorized, ErrForbidden, ErrInvalidInput,
// or any other error), redirects, context cancellation, or deadline expiry;
// the loader package's RedirectError is a control-flow outcome and carries no
// data worth caching. Action mutation responses are never cached. Negative
// caching may arrive later behind its own explicit policy; nothing in this
// contract supports or encourages it today.
//
// # Sensitive data
//
// A cache key derived only from a route ID or URL is NOT safe for
// user-specific results: /users/42 means different things to different
// callers, and identical URLs do not imply identical authorization. When a
// cached value depends on who is asking, the caller MUST fold the relevant
// scope (user, tenant, role) into the key components — see NewKey. Keys are
// compared, logged, and potentially persisted by future backends, so raw
// passwords, tokens, and other secrets must never appear in them; store a
// stable hash of such values instead. Entries must never retain request
// contexts, cookies, or other request-bound objects — with byte values they
// cannot.
//
// # Concurrency
//
// Every Store implementation MUST be safe for concurrent use: Get, Set, and
// Delete may be called from many goroutines at once, including against the
// same key. Individual operations are atomic — no caller observes a partial
// entry, and a Set whose call has returned is visible to every Get that starts
// afterwards. Operations on unrelated keys do not affect each other's results.
// Expiry and eviction must preserve these guarantees: an entry is either
// wholly present or wholly absent. The contract promises per-process
// consistency only; it does not promise distributed linearizability, and
// backends that cannot meet it must say so in their own documentation.
//
// # Ownership and copying
//
// Implementations MUST give entries copy-in/copy-out semantics: Set copies
// Value before storing, and Get returns a fresh copy. After Set returns, the
// caller may reuse or mutate its slice freely; after Get returns, the caller
// may mutate the returned bytes freely — neither affects what the store holds.
// Implementations that instead hand out guarded read-only views must prove
// ordinary callers cannot mutate shared state; simply documenting a []byte as
// "immutable" is not sufficient.
//
// # Contexts
//
// Store methods take a context.Context because future backends may be remote
// (network-bound); a fast in-process store ignores it past the initial checks.
// The rules, which implementations MUST follow:
//
//   - A nil context is rejected with ErrNilContext; it is never silently
//     treated as context.Background. This mirrors loader.Execute and
//     action.Execute.
//   - An already-canceled context yields its own error (context.Canceled or
//     context.DeadlineExceeded) with no side effects, and ok=false for Get.
//   - Cancellation during an in-flight operation is best-effort cooperative:
//     the operation either completes (and reports success) or returns the
//     context error. A canceled operation never claims success for work that
//     did not happen, and canceling after a Set completes does not roll it
//     back.
//   - No cache operation spawns request-bound background goroutines, and no
//     Fiber request object is retained past a call's return.
//
// # Future work
//
// E04 adds explicit invalidation
// building on Delete and, if needed, a higher-level index; it must not require
// breaking changes to Store. E05 wraps misses with singleflight coordination
// outside the store; the store itself never invokes loader functions.
//
// The package imports only the standard library. It has no Fiber, fasthttp,
// templ, Node, generated-registry, or application-package dependencies, and
// can be imported by external Go modules.
package cache

import (
	"context"
	"strconv"
	"strings"
	"unicode/utf8"
)

// MaxKeyLength bounds cache keys. It exists so key construction fails early
// at an absurd size rather than turning every key into an unbounded memory
// commitment inside a bounded store. 2048 bytes comfortably fits a route
// identity plus typed parameters, a query digest, and a user/tenant scope
// component, while remaining cheap to hold per entry.
const MaxKeyLength = 2048

// maxNamespaceLength bounds the namespace component of NewKey independently,
// since namespaces name logical domains and are expected to be short.
const maxNamespaceLength = 128

// Key identifies a cache entry. Its zero value is invalid. Construct keys
// with NewKey when combining multiple components; a hand-written literal is
// acceptable when it is a single opaque token, and must pass Validate.
//
// Keys are deterministic identities, not URLs: nothing recomputes route IDs
// from them, and no HTTP route is parsed to produce them (that is E03's job).
// Two stores hold disjoint namespaces only by construction — a key built for
// one purpose never collides with a key built for another because NewKey
// includes the namespace as its first length-prefixed component.
type Key string

// Validate reports whether k is a usable cache key. A valid key is non-empty,
// at most MaxKeyLength bytes, valid UTF-8, and free of ASCII control
// characters (bytes below 0x20 and 0x7f). The control-character rule keeps
// keys safe to log, compare, and hand to future file- or network-backed
// stores; binary components must be hex- or base64-encoded by the caller.
// Whitespace at the edges is rejected: a key with surrounding whitespace is
// almost always a construction bug.
func (k Key) Validate() error {
	if k == "" {
		return errInvalidKeyBytes("key is empty")
	}
	if len(k) > MaxKeyLength {
		return errInvalidKeyBytes("key is " + strconv.Itoa(len(k)) + " bytes, exceeds the maximum of " + strconv.Itoa(MaxKeyLength))
	}
	if !utf8.ValidString(string(k)) {
		return errInvalidKeyBytes("key is not valid UTF-8")
	}
	if strings.TrimSpace(string(k)) != string(k) {
		return errInvalidKeyBytes("key has leading or trailing whitespace")
	}
	for i := 0; i < len(k); i++ {
		if k[i] < 0x20 || k[i] == 0x7f {
			return errInvalidKeyBytes("key contains a control character")
		}
	}
	return nil
}

// String returns the key text. It exists for logging and debugging; treat it
// as potentially sensitive (see the Sensitive data section of the package
// documentation).
func (k Key) String() string { return string(k) }

// NewKey builds a Key from a namespace and any number of ordered components.
// It exists to make the common key shapes collision-proof; callers with a
// single opaque token may construct Key directly.
//
// The namespace names the logical domain of the entry ("loader:user-profile",
// "image:thumbnail", "application:settings") and is the first line of
// isolation between unrelated caches. It must be non-empty, valid UTF-8, free
// of control characters, at most 128 bytes, and carry no leading or trailing
// whitespace.
//
// Components are joined with length prefixes, NOT separators, so distinct
// component sequences can never produce the same key: NewKey("n", "a:b", "c")
// differs from NewKey("n", "a", "b:c"), which plain separator joining cannot
// guarantee. Encoding is by UTF-8 byte length, so multi-byte runes are
// handled exactly, and an empty component is distinct from an absent one
// (NewKey("n", "") ends with "0:" while NewKey("n") does not). This property
// is what lets E05 treat identical keys as identical concurrent misses.
//
// NewKey validates the namespace and the final assembled key, returning an
// error rather than a silently-invalid key. Component content is otherwise not
// restricted beyond what Key.Validate enforces on the result; when a component
// holds binary or secret material, pass its hex or SHA-256 digest instead.
//
// This is a pure function: it allocates no state, starts nothing, and touches
// no clock.
func NewKey(namespace string, components ...string) (Key, error) {
	if namespace == "" {
		return "", errInvalidKeyBytes("namespace is empty")
	}
	if !utf8.ValidString(namespace) {
		return "", errInvalidKeyBytes("namespace is not valid UTF-8")
	}
	if strings.TrimSpace(namespace) != namespace {
		return "", errInvalidKeyBytes("namespace has leading or trailing whitespace")
	}
	if len(namespace) > maxNamespaceLength {
		return "", errInvalidKeyBytes("namespace exceeds " + strconv.Itoa(maxNamespaceLength) + " bytes")
	}
	for i := 0; i < len(namespace); i++ {
		if namespace[i] < 0x20 || namespace[i] == 0x7f {
			return "", errInvalidKeyBytes("namespace contains a control character")
		}
	}
	var b strings.Builder
	writeComponent(&b, namespace)
	for _, c := range components {
		writeComponent(&b, c)
	}
	key := Key(b.String())
	if err := key.Validate(); err != nil {
		return "", err
	}
	return key, nil
}

// writeComponent appends the length-prefixed encoding of s to b: the decimal
// UTF-8 byte length, a colon, then the bytes. Lengths, not delimiters, carry
// the structure, so no two distinct component sequences encode alike.
func writeComponent(b *strings.Builder, s string) {
	b.WriteString(strconv.Itoa(len(s)))
	b.WriteByte(':')
	b.WriteString(s)
}

// Entry is a cacheable value. Value may be empty or nil — an empty value is a
// perfectly meaningful cached result (an empty list, a JSON null serialized as
// "null") and reads back as a hit, not a miss. What an entry does not carry:
// an expiry (that belongs to the Policy given to Set and the store's internal
// bookkeeping), a key (entries do not know their own identity), and any type
// information (the interpretation of the bytes belongs to the caller that
// produced them).
//
// Entry is a plain struct; the copy-in/copy-out obligation sits on Store
// implementations, which must not alias the caller's slice.
type Entry struct {
	Value []byte
}

// Store is the cache contract. It is a data-management interface, not an HTTP
// middleware: nothing in it assumes cached values represent responses, and
// it carries no status codes or headers. Implementations must satisfy the
// package-level Concurrency, Ownership and copying, and Contexts rules, and
// must reject keys failing Key.Validate with an error wrapping
// ErrInvalidKey on every method — a caller bug surfaces as an error, never
// as a silently wrong result.
//
// The zero-value question ("what does an unconfigured Store do?") is answered
// by construction conventions, not the interface: a caller that wants no
// caching simply does not construct a store. There is no global default
// store, and this package provides none.
type Store interface {
	// Get returns the entry stored under key.
	//
	// The three outcomes are distinguished exactly:
	//
	//   (entry, true, nil)  hit: entry was present and not expired. The
	//                       value is a private copy owned by the caller (see
	//                       Ownership and copying). An empty Value is a hit.
	//   (zero,  false, nil) miss: no live entry under key — either never
	//                       written, deleted, or expired. A miss is an
	//                       ordinary outcome, never an operational error.
	//   (zero,  false, err) error: the lookup itself failed. Callers must not
	//                       treat this as a miss; converting a store error
	//                       into a silent miss masks real faults.
	//
	// Expired entries are misses by definition; no stale value is returned
	// alongside the expiry signal. Get never mutates the stored entry.
	Get(ctx context.Context, key Key) (Entry, bool, error)

	// Set stores entry under key with policy. Implementations copy
	// entry.Value; after Set returns, caller mutations of that slice do not
	// affect the store. A store configured with an entry-size bound (a
	// construction-time concern, not a Policy field) rejects an oversized
	// entry with an error wrapping ErrEntryTooLarge and stores nothing.
	//
	// Set overwrites any previous entry under the same key. Policy.Validate
	// errors are returned to the caller (wrapping ErrInvalidPolicy) rather
	// than silently approximated — a caller asking for a TTL of -1s gets an
	// error, not a guess. If ctx is already canceled, Set returns the context
	// error without storing anything; cancellation arriving after the write
	// completed does not undo it.
	Set(ctx context.Context, key Key, entry Entry, policy Policy) error

	// Delete removes the entry under key. Deleting a missing key is a
	// successful no-op (returns nil), not an error: exact-key invalidation
	// must be idempotent so E04 can invalidate speculatively. Namespace- or
	// tag-scoped deletion is NOT part of this interface; higher-level
	// invalidation layers (E04) build indexes on top of exact-key deletes
	// rather than requiring every backend to support scans.
	Delete(ctx context.Context, key Key) error
}

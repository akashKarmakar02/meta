package cache

import (
	"errors"
	"fmt"
)

// Error categories for the cache contract. They are deliberately few and
// deliberately carry no HTTP meaning: no status codes, no header semantics,
// no response writing. Classify with errors.Is; implementations wrap these
// sentinels (%w) with their own context so errors.As still reaches the cause.
// Ordinary Go errors that fit none of them pass through unchanged.
var (
	// ErrInvalidKey reports a key that fails Key.Validate or a namespace
	// rejected by NewKey. It is a caller bug, not an operational fault.
	ErrInvalidKey = errors.New("cache: invalid key")

	// ErrInvalidPolicy reports a policy rejected by Policy.Validate (zero,
	// negative, non-finite, or over-max TTL). A store returns it from Set
	// instead of storing under an unspecified lifetime.
	ErrInvalidPolicy = errors.New("cache: invalid policy")

	// ErrEntryTooLarge reports an entry whose Value exceeds the store's
	// construction-time size bound. The entry was not stored; nothing else
	// was evicted on its behalf (E02 decides its own admission policy).
	ErrEntryTooLarge = errors.New("cache: entry too large for store")

	// ErrClosed reports an operation on a closed store. Stores that have no
	// close step never return it.
	ErrClosed = errors.New("cache: store is closed")

	// ErrNilContext reports a nil context.Context passed to a Store method,
	// matching loader.Execute and action.Execute. Callers must never let a
	// nil context silently behave like context.Background.
	ErrNilContext = errors.New("cache: nil context")
)

// errInvalidKeyBytes constructs an ErrInvalidKey error with the offending
// detail; it is unexported so the category stays exactly one sentinel.
func errInvalidKeyBytes(detail string) error {
	return fmt.Errorf("%w: %s", ErrInvalidKey, detail)
}

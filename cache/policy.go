package cache

import (
	"fmt"
	"time"
)

// MaxTTL bounds a Policy's TTL. It is 24 hours — generous for loader results
// (which refresh by re-render) while keeping any key-stability mistake from
// pinning stale data indefinitely. Values above it fail Validate, because an
// application asking for a year-long TTL almost always wants the entry to be
// versioned by key instead (a content digest in the key), which stays correct
// when the data changes, whereas TTL-cached data goes stale the moment its
// source does.
const MaxTTL = 24 * time.Hour

// Policy states how long a Set entry may live. This is the only knob E01
// exposes: TTL. Capacity, entry-size, and eviction limits are construction
// concerns of the concrete store (E02), not fields here — a policy describes
// an entry's lifetime, not the engine's shape.
//
// The zero Policy is invalid. Caching is strictly opt-in: there is no
// implicit default TTL, and "no policy" means "do not cache" (the caller
// simply does not call Set). A zero TTL or negative TTL means the same thing —
// do not cache this — but as an explicit construction error rather than a
// silent skip, so a forgotten TTL can never accidentally disable caching that
// was intended, nor enable caching that was not.
type Policy struct {
	// TTL is how long the entry may be served after it is stored. Expiry is
	// measured from the moment of Set using a monotonic clock where the
	// implementation has one (implementations must not compare wall-clock
	// times across a system clock adjustment). At exactly TTL elapsed the
	// entry is expired: the last served instant is the half-open interval
	// [stored, stored+TTL), and reads at or after stored+TTL are misses.
	TTL time.Duration
}

// Validate reports whether p is a usable cache policy. Valid policies carry a
// finite positive TTL at most MaxTTL. Errors wrap ErrInvalidPolicy and name
// the offending value in the message. Zero, negative, non-finite, and
// over-max TTLs are rejected; see Policy for the reasoning. Validate never
// panics and allocates nothing on success.
func (p Policy) Validate() error {
	if p.TTL <= 0 {
		return fmt.Errorf("%w: TTL must be positive, got %s", ErrInvalidPolicy, p.TTL)
	}
	if p.TTL > MaxTTL {
		return fmt.Errorf("%w: TTL %s exceeds the maximum of %s", ErrInvalidPolicy, p.TTL, MaxTTL)
	}
	return nil
}

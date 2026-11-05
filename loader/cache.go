package loader

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"

	"github.com/akashKarmakar02/meta/cache"
	"github.com/akashKarmakar02/meta/routecontext"
	"github.com/akashKarmakar02/meta/routeparams"
)

// Codec serializes and restores a loader result. Callbacks must be safe for
// concurrent use, Encode must return owned bytes, and Decode must return
// independently owned mutable values.
// Applications must version their format when it changes.
type Codec[T any] struct {
	Encode func(T) ([]byte, error)
	Decode func([]byte) (T, error)
}

// JSONCodec returns a typed JSON codec. JSON cannot faithfully round-trip all
// Go values; values failing the adapter's round-trip equality check bypass it.
func JSONCodec[T any]() Codec[T] {
	return Codec[T]{
		Encode: func(v T) ([]byte, error) { return json.Marshal(v) },
		Decode: func(b []byte) (T, error) { var v T; err := json.Unmarshal(b, &v); return v, err },
	}
}

// CacheEvent reports a cache operation without exposing keys, values or errors.
type CacheEvent string

const (
	CacheHit         CacheEvent = "hit"
	CacheMiss        CacheEvent = "miss"
	CacheBypass      CacheEvent = "bypass"
	CacheReadError   CacheEvent = "read_error"
	CacheWriteError  CacheEvent = "write_error"
	CacheDecodeError CacheEvent = "decode_error"
	CacheEncodeError CacheEvent = "encode_error"
	CacheDeleteError CacheEvent = "delete_error"
	CacheClosed      CacheEvent = "closed"
	// E04 events:
	CacheInvalidated         CacheEvent = "invalidated"
	CacheInvalidationFailed  CacheEvent = "invalidation_failed"
	CacheStaleFillSuppressed CacheEvent = "stale_fill_suppressed"
	CacheFlightCreated       CacheEvent = "flight_created"
	CacheFlightJoined        CacheEvent = "flight_joined"
)

// CacheOptions explicitly configures one loader's cache. Scope must affirm
// eligibility and identify all authorization, tenant and other dependencies
// beyond route parameters and query; return false to bypass. Never use secrets
// as scope. Callbacks must be concurrency-safe. Store lifetime stays with the
// application; the loader never closes it.
type CacheOptions[T any] struct {
	Store   cache.Store
	Policy  cache.Policy
	Scope   func(context.Context, routecontext.Route) (scope string, cacheable bool, err error)
	Codec   Codec[T]
	OnEvent func(CacheEvent)
	// Invalidator (E04, optional) attaches the store's invalidation
	// coordinator: fills fence their writes against coordinated
	// invalidations, and Invalidator.Invalidate safely targets this binding.
	// Without it the binding behaves exactly as E03 (unfenced). Applications
	// that invalidate MUST attach it on every cached binding over the store:
	// a binding without it stays outside the stale-fill guarantee. The
	// invalidator's store must equal Store; NewRegistry rejects a mismatch.
	Invalidator *Invalidator
	// Flights opts into process-local coalescing. The group must use this same
	// invalidator. Shared loaders receive only route metadata and the group's
	// deadline, not request-specific context values.
	Flights *FlightGroup
}

var ErrInvalidCacheConfig = errors.New("loader: invalid cache configuration")

// LoaderCacheKey constructs the exact key used by BindCached. All decoded path
// parameters and query keys are sorted by name; repeated query values keep
// their order. Scope must be nonempty and application-validated. This helper
// may also be used with Store.Delete for an exact key.
func LoaderCacheKey(route routecontext.Route, scope string) (cache.Key, error) {
	if scope == "" {
		return "", fmt.Errorf("%w: empty scope", cache.ErrInvalidKey)
	}
	if route.RouteID == "" {
		return "", fmt.Errorf("%w: empty route ID", cache.ErrInvalidKey)
	}
	params := route.Params.All()
	query := route.Query.All()
	parts := []string{route.RouteID, scope, strconv.Itoa(len(params))}
	keys := make([]string, 0, len(params))
	for name := range params {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, name := range keys {
		parts = append(parts, name, params[name])
	}
	keys = keys[:0]
	for name := range query {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	parts = append(parts, strconv.Itoa(len(keys)))
	for _, name := range keys {
		values := query[name]
		parts = append(parts, name, strconv.Itoa(len(values)))
		parts = append(parts, values...)
	}
	return cache.NewKey("loader", parts...)
}

// BindCached opts a typed loader into caching. NewRegistry rejects invalid
// configuration. Only successful, losslessly round-tripping results are
// stored; concurrent misses may execute the loader independently.
func BindCached[T any](routeID string, load Loader[T], opts CacheOptions[T]) Binding {
	binding := Bind(routeID, load)
	if load == nil {
		return binding
	}
	if nilStore(opts.Store) || opts.Scope == nil || opts.Codec.Encode == nil || opts.Codec.Decode == nil {
		binding.configErr = fmt.Errorf("%w: store, scope and codec are required", ErrInvalidCacheConfig)
		return binding
	}
	if err := opts.Policy.Validate(); err != nil {
		binding.configErr = fmt.Errorf("%w: %w", ErrInvalidCacheConfig, err)
		return binding
	}
	if opts.Flights != nil && (opts.Invalidator == nil || opts.Flights.invalidator != opts.Invalidator || opts.Flights.timeout <= 0) {
		binding.configErr = fmt.Errorf("%w: flight group must share the binding invalidator", ErrInvalidCacheConfig)
		return binding
	}
	identity := &flightBinding{marker: 1}
	// E04: an attached invalidator must coordinate the very same store this
	// binding writes to; a mismatch would fence some writes while invalidations
	// deleted from elsewhere — rejected at construction.
	if opts.Invalidator != nil && !sameStore(opts.Invalidator.store, opts.Store) {
		binding.configErr = fmt.Errorf("%w: cache options attach an invalidator built for a different store", ErrInvalidCacheConfig)
		return binding
	}
	emit := func(event CacheEvent) {
		if opts.OnEvent != nil {
			opts.OnEvent(event)
		}
	}
	binding.invoke = func(ctx context.Context, route routecontext.Route) (any, error) {
		if ctx == nil {
			return nil, ErrNilContext
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		scope, ok, err := opts.Scope(ctx, route)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil {
			return nil, fmt.Errorf("loader: cache scope: %w", err)
		}
		if !ok {
			emit(CacheBypass)
			return Execute(ctx, route, load)
		}
		key, err := LoaderCacheKey(route, scope)
		if err != nil {
			return nil, fmt.Errorf("loader: cache key: %w", err)
		}
		// E04 fencing: register this fill before reading the store. An
		// invalidation completing while the fill is in flight advances the
		// generation; the fill's later write observes it and is suppressed
		// instead of repopulating stale data. The reference is held across
		// loader execution and released when the fill finishes.
		var (
			fillKF  *keyFence
			fillGen uint64
		)
		if opts.Invalidator != nil {
			fillKF, fillGen = opts.Invalidator.fence.beginFill(key)
			defer opts.Invalidator.fence.release(fillKF)
		}
		entry, hit, err := opts.Store.Get(ctx, key)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, cache.ErrInvalidKey) || errors.Is(err, cache.ErrInvalidPolicy) {
			return nil, err
		}
		if err != nil {
			if errors.Is(err, cache.ErrClosed) {
				emit(CacheClosed)
			}
			emit(CacheReadError)
		} else if hit {
			value, decodeErr := opts.Codec.Decode(entry.Value)
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if decodeErr == nil {
				emit(CacheHit)
				return value, nil
			}
			emit(CacheDecodeError)
			var deleteErr error
			if opts.Invalidator != nil {
				deleteErr = opts.Invalidator.fence.deleteIfCurrent(fillKF, fillGen, ctx, opts.Store, key)
			} else {
				deleteErr = opts.Store.Delete(ctx, key)
			}
			if errors.Is(deleteErr, ErrStaleFill) {
				deleteErr = nil
			}
			if deleteErr != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				if errors.Is(deleteErr, context.Canceled) || errors.Is(deleteErr, context.DeadlineExceeded) || errors.Is(deleteErr, cache.ErrInvalidKey) || errors.Is(deleteErr, cache.ErrInvalidPolicy) {
					return nil, deleteErr
				}
				if errors.Is(deleteErr, cache.ErrClosed) {
					emit(CacheClosed)
				}
				emit(CacheDeleteError)
			}
		} else {
			emit(CacheMiss)
		}
		if opts.Flights != nil {
			result, claimed, waitErr := opts.Flights.wait(ctx, key, fillKF, fillGen, identity, emit, func(shared context.Context) flightResult {
				shared = routecontext.WithContext(shared, route)
				shared = routeparams.WithContext(shared, route.Params)
				// A fill may have completed after the original cache miss.
				recheck, found, readErr := opts.Store.Get(shared, key)
				if shared.Err() != nil {
					return flightResult{err: shared.Err()}
				}
				if errors.Is(readErr, context.Canceled) || errors.Is(readErr, context.DeadlineExceeded) || errors.Is(readErr, cache.ErrInvalidKey) || errors.Is(readErr, cache.ErrInvalidPolicy) {
					return flightResult{err: readErr}
				}
				if readErr != nil {
					if errors.Is(readErr, cache.ErrClosed) {
						emit(CacheClosed)
					}
					emit(CacheReadError)
				} else if found {
					decoded, decodeErr := opts.Codec.Decode(bytes.Clone(recheck.Value))
					if shared.Err() != nil {
						return flightResult{err: shared.Err()}
					}
					if decodeErr == nil {
						emit(CacheHit)
						return flightResult{value: decoded, encoded: bytes.Clone(recheck.Value), shareable: true}
					}
					emit(CacheDecodeError)
					deleteErr := opts.Invalidator.fence.deleteIfCurrent(fillKF, fillGen, shared, opts.Store, key)
					if shared.Err() != nil {
						return flightResult{err: shared.Err()}
					}
					if errors.Is(deleteErr, context.Canceled) || errors.Is(deleteErr, context.DeadlineExceeded) || errors.Is(deleteErr, cache.ErrInvalidKey) || errors.Is(deleteErr, cache.ErrInvalidPolicy) {
						return flightResult{err: deleteErr}
					}
					if deleteErr != nil && !errors.Is(deleteErr, ErrStaleFill) {
						if errors.Is(deleteErr, cache.ErrClosed) {
							emit(CacheClosed)
						}
						emit(CacheDeleteError)
					}
				}
				return sharedFill(shared, route, key, fillKF, fillGen, opts, load, emit)
			})
			if waitErr != nil {
				return nil, waitErr
			}
			if result.err != nil {
				return nil, result.err
			}
			if !result.shareable {
				if claimed {
					return result.value, nil
				}
				// A codec cannot safely copy this result. Other waiters each
				// execute once independently, preserving E03 result semantics.
				return Execute(ctx, route, load)
			}
			decoded, decodeErr := opts.Codec.Decode(bytes.Clone(result.encoded))
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if decodeErr != nil {
				emit(CacheDecodeError)
				return nil, fmt.Errorf("loader: shared result decode: %w", decodeErr)
			}
			return decoded, nil
		}
		value, err := Execute(ctx, route, load)
		if err != nil {
			return nil, err
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		encoded, err := opts.Codec.Encode(value)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil {
			emit(CacheEncodeError)
			return value, nil
		}
		restored, err := opts.Codec.Decode(bytes.Clone(encoded))
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil || !reflect.DeepEqual(value, restored) {
			emit(CacheEncodeError)
			return value, nil
		}
		// E04 fenced write: the generation check and the Set share one
		// critical section with invalidation's advance+delete section on the
		// same key fence. A fenced fill whose write was suppressed by an
		// invalidation returns ErrStaleFill; the loader's result is still
		// served to this request.
		if opts.Invalidator != nil {
			err = opts.Invalidator.fence.storeIfCurrent(fillKF, fillGen, ctx, opts.Store, key, cache.Entry{Value: encoded}, opts.Policy)
			if errors.Is(err, ErrStaleFill) {
				emit(CacheStaleFillSuppressed)
				return value, nil
			}
		} else {
			err = opts.Store.Set(ctx, key, cache.Entry{Value: encoded}, opts.Policy)
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, cache.ErrInvalidKey) || errors.Is(err, cache.ErrInvalidPolicy) {
			return nil, err
		}
		if err != nil {
			if errors.Is(err, cache.ErrClosed) {
				emit(CacheClosed)
			}
			emit(CacheWriteError)
		}
		return value, nil
	}
	return binding
}

func nilStore(store cache.Store) bool {
	if store == nil {
		return true
	}
	value := reflect.ValueOf(store)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// sharedFill executes and writes once for a flight, using only the flight's
// independent, bounded context. Successful results are encoded once and each
// waiter decodes its own copy.
func sharedFill[T any](ctx context.Context, route routecontext.Route, key cache.Key, fence *keyFence, generation uint64, opts CacheOptions[T], load Loader[T], emit func(CacheEvent)) flightResult {
	value, err := Execute(ctx, route, load)
	if err != nil {
		return flightResult{err: err}
	}
	if err := ctx.Err(); err != nil {
		return flightResult{err: err}
	}
	encoded, err := opts.Codec.Encode(value)
	if ctx.Err() != nil {
		return flightResult{err: ctx.Err()}
	}
	if err != nil {
		emit(CacheEncodeError)
		return flightResult{value: value}
	}
	restored, err := opts.Codec.Decode(bytes.Clone(encoded))
	if ctx.Err() != nil {
		return flightResult{err: ctx.Err()}
	}
	if err != nil || !reflect.DeepEqual(value, restored) {
		emit(CacheEncodeError)
		return flightResult{value: value}
	}
	err = opts.Invalidator.fence.storeIfCurrent(fence, generation, ctx, opts.Store, key, cache.Entry{Value: encoded}, opts.Policy)
	if errors.Is(err, ErrStaleFill) {
		emit(CacheStaleFillSuppressed)
		return flightResult{encoded: bytes.Clone(encoded), shareable: true}
	}
	if ctx.Err() != nil {
		return flightResult{err: ctx.Err()}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, cache.ErrInvalidKey) || errors.Is(err, cache.ErrInvalidPolicy) {
		return flightResult{err: err}
	}
	if err != nil {
		if errors.Is(err, cache.ErrClosed) {
			emit(CacheClosed)
		}
		emit(CacheWriteError)
	}
	return flightResult{encoded: bytes.Clone(encoded), shareable: true}
}

// sameStore avoids panicking on interface values whose dynamic type is not
// comparable; such stores cannot safely participate in a coordinated domain.
func sameStore(a, b cache.Store) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	ta, tb := reflect.TypeOf(a), reflect.TypeOf(b)
	if ta != tb || !ta.Comparable() || !reflect.ValueOf(a).Comparable() || !reflect.ValueOf(b).Comparable() {
		return false
	}
	return a == b
}

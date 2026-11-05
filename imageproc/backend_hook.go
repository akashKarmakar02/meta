package imageproc

import (
	"context"
	"reflect"
	"sync"
)

// backendFactory builds the production processor. It is installed once at
// program init by the internal native adapter (or by any embedder that
// compiled one in), keeping this public package free of CGO and native
// dependencies: importing go-meta/imageproc alone links nothing.
var (
	backendMu      sync.Mutex
	backendFactory func(ctx context.Context) (Processor, error)
)

// RegisterBackend installs the production processor constructor. It is
// intended for framework wiring (internal packages) and must be called
// before New. Registering a different constructor after one is installed
// is a programming error and panics — deterministic and early rather than
// silently replaced; registering the same constructor again is a no-op
// (idempotent wiring, safe for repeated test setup).
func RegisterBackend(f func(ctx context.Context) (Processor, error)) {
	if f == nil {
		panic("imageproc: RegisterBackend(nil)")
	}
	backendMu.Lock()
	defer backendMu.Unlock()
	if backendFactory == nil {
		backendFactory = f
		return
	}
	// Same function value re-registering is a no-op; anything else is a
	// conflict. Function values are only equal when both are nil, so compare
	// via reflect on the underlying pointers.
	if !sameFunc(backendFactory, f) {
		panic("imageproc: a different backend is already registered")
	}
}

// newBackend resolves the installed factory or reports ErrBackendUnavailable.
func newBackend(ctx context.Context) (Processor, error) {
	backendMu.Lock()
	f := backendFactory
	backendMu.Unlock()
	if f == nil {
		return nil, ErrBackendUnavailable
	}
	return f(ctx)
}

// sameFunc reports whether two constructor values are the same function.
func sameFunc(a, b func(ctx context.Context) (Processor, error)) bool {
	return reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer()
}

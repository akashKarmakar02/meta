package imageproc

import (
	"context"
	"errors"
	"fmt"
)

// ErrBackendUnavailable reports that the native backend cannot start:
// missing shared libraries, incompatible versions, or a failed probe.
// The HTTP layer must surface it as a server configuration problem, never
// as a per-request image failure.
var ErrBackendUnavailable = errors.New("imageproc: backend unavailable")

// ErrSourceInvalid reports source bytes the backend rejected at its own
// boundary (unknown signature, unsupported input format).
var ErrSourceInvalid = errors.New("imageproc: invalid source image")

// ErrSourceTooLarge is reserved for F04's resource limits; defined here so
// the error taxonomy is complete and stable before F03/F04 build on it.
var ErrSourceTooLarge = errors.New("imageproc: source exceeds limits")

// ErrClosed reports use of a processor after Close.
var ErrClosed = errors.New("imageproc: processor closed")

// classify maps implementation errors onto the public taxonomy. It runs
// inside backends; HTTP text is chosen at the endpoint layer only.
func classify(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, context.Canceled):
		return err
	case errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, ErrUnsupportedFormat),
		errors.Is(err, ErrInvalidTransform),
		errors.Is(err, ErrProcessing),
		errors.Is(err, ErrSourceInvalid),
		errors.Is(err, ErrSourceTooLarge),
		errors.Is(err, ErrClosed):
		return err
	default:
		return fmt.Errorf("%w: %v", ErrProcessing, err)
	}
}

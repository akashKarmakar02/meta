package imageproc

import (
	"context"
	"fmt"
	"sync"
)

// Fake is a deterministic Processor for tests: no native libraries, no
// filesystem access, no Fiber dependency. It returns canned results,
// errors, or cancellation, and counts invocations. Safe for concurrent
// use; instances are not shared between tests by design.
type Fake struct {
	Caps     Capabilities
	Version  string // returned in results; defaults to "fake-v1"
	MIME     string // optional override; default derives from the format
	Err      error  // when non-nil, Process returns it after recording the call
	CheckCtx bool   // when true, Process checks ctx.Err() like a real backend

	mu    sync.Mutex
	calls []FakeCall
}

// FakeCall records one Process invocation.
type FakeCall struct {
	Source    Source
	Transform Transform
}

var (
	_ Processor = (*Fake)(nil)
	_ Processor = NoopProcessor{}
)

// NoopProcessor is the zero-dependency processor used when image
// optimization is intentionally disabled: it reports no capabilities and
// fails every Process call with ErrBackendUnavailable wrapped, so the F01
// endpoint keeps its truthful 501 until real processing exists.
type NoopProcessor struct{}

func (NoopProcessor) Capabilities() Capabilities { return Capabilities{} }

func (NoopProcessor) Process(context.Context, Source, Transform) (Result, error) {
	return Result{}, fmt.Errorf("image processor disabled: %w", ErrBackendUnavailable)
}

// NewFake returns a fake with default public capabilities.
func NewFake() *Fake {
	return &Fake{
		Caps: Capabilities{
			JPEGDecode: true, PNGDecode: true,
			JPEGEncode: true, PNGEncode: true, WebPEncode: true, AVIFEncode: true,
		},
		Version: "fake-v1",
	}
}

func (f *Fake) Capabilities() Capabilities {
	c := f.Caps
	return c
}

func (f *Fake) record(src Source, t Transform) {
	f.mu.Lock()
	f.calls = append(f.calls, FakeCall{Source: src, Transform: t})
	f.mu.Unlock()
}

// Calls returns a copy of the recorded invocations, including a copy of
// each source's bytes, so callers can never mutate recorded state.
func (f *Fake) Calls() []FakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]FakeCall, len(f.calls))
	for i, c := range f.calls {
		b := make([]byte, len(c.Source.Bytes))
		copy(b, c.Source.Bytes)
		c.Source.Bytes = b
		out[i] = c
	}
	return out
}

// CallCount reports how many Process invocations were recorded.
func (f *Fake) CallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *Fake) Process(ctx context.Context, src Source, t Transform) (Result, error) {
	if ctx == nil {
		return Result{}, context.Canceled
	}
	f.record(src, t)
	if f.CheckCtx {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
	}
	if f.Err != nil {
		return Result{}, f.Err
	}
	if err := ValidateTransform(t); err != nil {
		return Result{}, err
	}
	if !f.Caps.Encode(t.Format) {
		return Result{}, fmt.Errorf("%w: encode %s", ErrUnsupportedFormat, string(t.Format))
	}
	version := f.Version
	if version == "" {
		version = "fake-v1"
	}
	mime, err := t.Format.MIME()
	if err != nil {
		return Result{}, err
	}
	if f.MIME != "" {
		mime = f.MIME
	}
	w, h := t.Width, t.Height
	if w == 0 {
		w = h
	}
	if h == 0 {
		h = w
	}
	data := make([]byte, 16)
	for i := range data {
		data[i] = byte(i)
	}
	return Result{
		Data: data, Format: t.Format, MIME: mime,
		Width: w, Height: h, Version: version,
	}, nil
}

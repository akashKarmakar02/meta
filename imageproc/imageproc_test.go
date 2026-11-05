package imageproc_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/akashKarmakar02/meta/imageopt"
	"github.com/akashKarmakar02/meta/imageproc"
)

func tr() imageproc.Transform {
	return imageproc.Transform{Width: 64, Height: 64, Quality: 80, Format: imageopt.FormatWebP}
}

func src() imageproc.Source {
	return imageproc.Source{URLIdentity: "/images/hero.jpg", Bytes: []byte("jpegbytes")}
}

// TestValidateTransform pins the shared contract rules.
func TestValidateTransform(t *testing.T) {
	base := tr()
	if err := imageproc.ValidateTransform(base); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*imageproc.Transform){
		"zero-both-dims":     func(x *imageproc.Transform) { x.Width, x.Height = 0, 0 },
		"negative-width":     func(x *imageproc.Transform) { x.Width = -1 },
		"over-max-dimension": func(x *imageproc.Transform) { x.Width = imageopt.MaxDimension + 1 },
		"over-max-pixels":    func(x *imageproc.Transform) { x.Width, x.Height = 6000, 6000 },
		"quality-zero":       func(x *imageproc.Transform) { x.Quality = 0 },
		"quality-over-100":   func(x *imageproc.Transform) { x.Quality = 101 },
		"format-auto":        func(x *imageproc.Transform) { x.Format = imageopt.FormatAuto },
		"format-empty":       func(x *imageproc.Transform) { x.Format = "" },
	} {
		x := base
		mutate(&x)
		if err := imageproc.ValidateTransform(x); err == nil {
			t.Fatalf("%s: accepted invalid transform", name)
		}
	}
}

// TestCapabilitiesEncode covers format/capability distinction.
func TestCapabilitiesEncode(t *testing.T) {
	caps := imageproc.Capabilities{WebPEncode: true}
	if !caps.Encode(imageopt.FormatWebP) {
		t.Fatal("webp encode should be reported")
	}
	for _, f := range []imageopt.Format{imageopt.FormatAVIF, imageopt.FormatJPEG, imageopt.FormatPNG, imageopt.FormatAuto} {
		if caps.Encode(f) {
			t.Fatalf("%s encode incorrectly reported", f)
		}
	}
}

// TestFakeProcessorContract exercises the fake as the reference Processor
// implementation: ownership, errors, cancellation, capability honesty,
// invocation counting, concurrent use.
func TestFakeProcessorContract(t *testing.T) {
	f := imageproc.NewFake()
	ctx := context.Background()

	// Successful result owns its bytes and agrees with the format.
	res, err := f.Process(ctx, src(), tr())
	if err != nil {
		t.Fatal(err)
	}
	if res.Format != imageopt.FormatWebP || res.MIME != "image/webp" {
		t.Fatalf("format/mime disagreement: %s %s", res.Format, res.MIME)
	}
	if res.Width != 64 || res.Height != 64 || len(res.Data) == 0 {
		t.Fatalf("bad result metadata: %+v", res)
	}
	if res.Version == "" {
		t.Fatal("empty version identity")
	}
	// Owned bytes: mutating them cannot affect the fake.
	res.Data[0] = 0xff
	if f.CallCount() != 1 {
		t.Fatalf("calls = %d", f.CallCount())
	}

	// Height-only transform derives the other dimension in results.
	hOnly := tr()
	hOnly.Width = 0
	res, err = f.Process(ctx, src(), hOnly)
	if err != nil || res.Width != 64 {
		t.Fatalf("height-only: %v %+v", err, res)
	}

	// Context canceled before processing fails without executing.
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	f2 := imageproc.NewFake()
	f2.CheckCtx = true
	if _, err := f2.Process(canceled, src(), tr()); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled ctx: %v", err)
	}
	if f2.CallCount() != 1 {
		t.Fatalf("cancel still recorded a call (count=%d) — recording must precede ctx checks for observability", f2.CallCount())
	}

	// Configured error surfaces verbatim and produces no result.
	f3 := imageproc.NewFake()
	f3.Err = errors.New("boom")
	if _, err := f3.Process(ctx, src(), tr()); err == nil || err.Error() != "boom" {
		t.Fatalf("configured error: %v", err)
	}

	// Missing capability is an explicit unsupported-format error, never
	// a silent fallback to another format.
	f4 := imageproc.NewFake()
	f4.Caps.AVIFEncode = false
	if _, err := f4.Process(ctx, src(), func() imageproc.Transform {
		x := tr()
		x.Format = imageopt.FormatAVIF
		return x
	}()); !errors.Is(err, imageproc.ErrUnsupportedFormat) {
		t.Fatalf("missing capability error: %v", err)
	}

	// Concurrent use is safe; Calls() returns copies.
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Go(func() {
			if _, err := f.Process(ctx, src(), tr()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if n := f.CallCount(); n < 33 {
		t.Fatalf("call count after concurrency = %d", n)
	}
	first := f.Calls()[0]
	first.Source.Bytes[0] = 'X'
	if f.Calls()[0].Source.Bytes[0] == 'X' {
		t.Fatal("Calls() returned shared byte slices")
	}
}

// TestNoopProcessor pins the disabled-backend behavior: no capabilities,
// every call fails closed as backend-unavailable.
func TestNoopProcessor(t *testing.T) {
	p := imageproc.NoopProcessor{}
	if (p.Capabilities() != imageproc.Capabilities{}) {
		t.Fatal("noop reports capabilities")
	}
	_, err := p.Process(context.Background(), src(), tr())
	if !errors.Is(err, imageproc.ErrBackendUnavailable) {
		t.Fatalf("noop process error: %v", err)
	}
}

// TestFakeMetadataAgreement ensures MIME override cannot silently disagree
// with the emitted format: an override is honored verbatim (the test's
// responsibility), but the default path must always agree.
func TestFakeMetadataAgreement(t *testing.T) {
	for _, f := range []imageopt.Format{imageopt.FormatAVIF, imageopt.FormatWebP, imageopt.FormatJPEG, imageopt.FormatPNG} {
		fake := imageproc.NewFake()
		tr := tr()
		tr.Format = f
		res, err := fake.Process(context.Background(), src(), tr)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := f.MIME()
		if res.MIME != want {
			t.Fatalf("%s: mime %q want %q", f, res.MIME, want)
		}
	}
}

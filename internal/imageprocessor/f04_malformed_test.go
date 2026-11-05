//go:build imageproc_native

package imageprocessor

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/akashKarmakar02/meta/imageopt"
	"github.com/akashKarmakar02/meta/imageproc"
)

// animatedVP8X marks the animation flag on a VP8X-only WebP header.
func animatedVP8X(w, h int) []byte {
	out := vp8xHeaderWebP(w, h)
	out[20] = 0x02 // VP8X flags bit 1: animation
	return out
}

// F04 §34: deterministic malformed-input corpus. Every entry must be
// rejected safely — a catchable error, never a panic or process crash —
// and the processor must stay usable after each. Entries are tiny (<200
// bytes); none declares over-limit dimensions (the pre-decode gate rows in
// f03r_predecode_test.go cover those).

func TestF04MalformedCorpus(t *testing.T) {
	n := f03Backend(t)

	validPNGBytes := validPNG(t)
	validJPEG := makeSource(t, 64, 48)

	corpus := map[string][]byte{
		"truncated-jpeg":             validJPEG[:len(validJPEG)/2],
		"junk-bytes":                 []byte("this is not an image at all, just text padding to look like one"),
		"random-binary":              []byte{0x37, 0x92, 0x00, 0xff, 0xd8, 0x00, 0x10, 0x00, 0x00, 0x00, 0x0f},
		"svg-with-image-magic":       []byte("<svg xmlns='http://www.w3.org/2000/svg'><image href='/etc/passwd'/></svg>"),
		"png-sig-only":               validPNGBytes[:8],
		"png-truncated-ihdr":         validPNGBytes[:20],
		"jpeg-soi-only":              {0xff, 0xd8},
		"jpeg-soi-eof-no-size":       {0xff, 0xd8, 0xff, 0xd9},
		"gif-family":                 validGIF(),
		"riff-but-not-webp":          []byte("RIFF\x00\x00\x00\x00WAVEfmt "),
		"webp-vp8x-only":             vp8xHeaderWebP(100, 100),
		"animated-webp-header":       animatedVP8X(32, 32),
		"webp-bomb-headers":          vp8xHeaderWebP(30000, 30000),
		"bmff-not-avif":              []byte("\x00\x00\x00\x18ftypisom\x00\x00\x02\x00isomiso2"),
		"avif-brand-empty-container": []byte("\x00\x00\x00\x14ftypavif\x00\x00\x00\x00avifmif1"),
		"valid-png-trailing-garbage": append(append([]byte{}, validPNGBytes...), []byte("GARBAGE TRAILER")...),
		"valid-jpeg-truncated-tail":  validJPEG[:len(validJPEG)-10],
		"mixed-magic-png-then-jpeg":  append(validPNGBytes[:8], validJPEG...),
		"nul-prefix":                 append([]byte{0x00, 0x00}, validPNGBytes...),
	}

	names := make([]string, 0, len(corpus))
	for name := range corpus {
		names = append(names, name)
	}
	// Deterministic order.
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}

	for _, name := range names {
		src := corpus[name]
		t.Run(name, func(t *testing.T) {
			// Must not panic; convert a panic into a test failure with the
			// corpus entry named.
			func() {
				defer func() {
					if p := recover(); p != nil {
						t.Fatalf("corpus %q panicked processor: %v", name, p)
					}
				}()
				_, _ = n.Process(context.Background(),
					imageproc.Source{Bytes: src},
					imageproc.Transform{Width: 32, Quality: 80, Format: imageopt.FormatWebP})
				_, _ = n.DecodeConfig(imageproc.Source{Bytes: src})
			}()
			// Processor usable afterward.
			res, err := n.Process(context.Background(),
				imageproc.Source{Bytes: validPNGBytes}, imageproc.Transform{Width: 32, Quality: 80, Format: imageopt.FormatWebP})
			if err != nil || len(res.Data) == 0 {
				t.Fatalf("processor unusable after %q: %v", name, err)
			}
		})
	}
}

// TestF04ValidImageWrongExtensionNaming: family detection is by magic
// bytes; filenames never matter (the Source carries no filename at all).
func TestF04ValidImageWrongExtensionNaming(t *testing.T) {
	n := f03Backend(t)
	// A valid JPEG labeled as nothing: bytes only.
	if _, err := n.Process(context.Background(),
		imageproc.Source{Bytes: makeSource(t, 64, 48)},
		imageproc.Transform{Width: 32, Quality: 80, Format: imageopt.FormatWebP}); err != nil {
		t.Fatalf("magic-byte detection failed on extensionless source: %v", err)
	}
}

// TestF04ConcurrentMalformedStress: interleaved valid + malformed requests
// under concurrency — no crash, all permits released, correct verdicts.
func TestF04ConcurrentMalformedStress(t *testing.T) {
	n := f03Backend(t)
	validPNG := validPNG(t)
	jpeg := makeSource(t, 64, 48)
	bad := [][]byte{
		[]byte("junk junk junk"),
		jpeg[:len(jpeg)/2],
		vp8xHeaderWebP(100, 100),
		[]byte{0xff, 0xd8, 0xff},
	}

	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				if (i+j)%2 == 0 {
					res, err := n.Process(context.Background(),
						imageproc.Source{Bytes: validPNG},
						imageproc.Transform{Width: 16, Quality: 60, Format: imageopt.FormatWebP})
					if err != nil || len(res.Data) == 0 {
						t.Errorf("valid request failed: %v", err)
						return
					}
				} else {
					src := bad[(i+j)%len(bad)]
					if _, err := n.Process(context.Background(),
						imageproc.Source{Bytes: src},
						imageproc.Transform{Width: 16, Quality: 60, Format: imageopt.FormatWebP}); err == nil {
						t.Errorf("malformed source accepted")
						return
					}
				}
			}
		}(i)
	}
	wg.Wait()
}

// TestF04SequentialFailuresRecover: repeated native encode failures do not
// wedge the processor (permit/cleanup regression check at processor level).
func TestF04SequentialFailuresRecover(t *testing.T) {
	n := f03Backend(t)
	for i := 0; i < 20; i++ {
		if _, err := n.Process(context.Background(),
			imageproc.Source{Bytes: []byte("bad")}, imageproc.Transform{Width: 16, Quality: 60, Format: imageopt.FormatWebP}); err == nil {
			t.Fatalf("bad source accepted on iteration %d", i)
		}
	}
	res, err := n.Process(context.Background(),
		imageproc.Source{Bytes: validPNG(t)}, imageproc.Transform{Width: 16, Quality: 60, Format: imageopt.FormatWebP})
	if err != nil || len(res.Data) == 0 {
		t.Fatalf("processor unusable after failures: %v", err)
	}
}

// TestF04CancellationAroundNativeCalls: canceled contexts before and
// immediately after native work leave the processor usable.
func TestF04CancellationAroundNativeCalls(t *testing.T) {
	n := f03Backend(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := n.Process(ctx,
		imageproc.Source{Bytes: validPNG(t)},
		imageproc.Transform{Width: 16, Quality: 60, Format: imageopt.FormatWebP}); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled: err = %v", err)
	}
	// Immediately usable.
	res, err := n.Process(context.Background(),
		imageproc.Source{Bytes: validPNG(t)}, imageproc.Transform{Width: 16, Quality: 60, Format: imageopt.FormatWebP})
	if err != nil || len(res.Data) == 0 {
		t.Fatalf("unusable after cancellation: %v", err)
	}
}

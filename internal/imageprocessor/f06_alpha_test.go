//go:build imageproc_native

package imageprocessor

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"go-meta/imageopt"
	"go-meta/imageproc"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestF06NativePackingPreservesStraightAlpha(t *testing.T) {
	img := image.NewNRGBA(image.Rect(7, 9, 8, 10))
	img.SetNRGBA(7, 9, color.NRGBA{R: 200, G: 100, B: 50, A: 128})
	got := toRGBA(img)
	want := []byte{200, 100, 50, 128}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("straight alpha: got %v want %v", got, want)
		}
	}
	backing := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	backing.SetNRGBA(2, 2, color.NRGBA{R: 200, G: 100, B: 50, A: 128})
	sub := backing.SubImage(image.Rect(2, 2, 3, 3))
	if got := toRGBA(sub); !bytes.Equal(got, want) {
		t.Fatalf("subimage: %v", got)
	}
	premultiplied := image.NewRGBA(image.Rect(0, 0, 1, 1))
	premultiplied.Set(0, 0, color.NRGBA{R: 200, G: 100, B: 50, A: 128})
	got = toRGBA(premultiplied)
	if got[0] < 197 || got[0] > 201 || got[3] != 128 {
		t.Fatalf("premultiplied input: %v", got)
	}
}

func TestF06AnimatedPNGRejectedBeforeDecode(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	raw := encoded.Bytes()
	// Insert a valid APNG animation-control chunk after the IHDR.
	payload := []byte{0, 0, 0, 2, 0, 0, 0, 0}
	chunk := make([]byte, 12+len(payload))
	binary.BigEndian.PutUint32(chunk[:4], uint32(len(payload)))
	copy(chunk[4:8], "acTL")
	copy(chunk[8:], payload)
	binary.BigEndian.PutUint32(chunk[8+len(payload):], crc32.ChecksumIEEE(chunk[4:8+len(payload)]))
	animated := append(bytes.Clone(raw[:33]), chunk...)
	animated = append(animated, raw[33:]...)
	n, err := New(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := n.DecodeConfig(imageproc.Source{Bytes: animated}); !errors.Is(err, imageproc.ErrSourceInvalid) {
		t.Fatalf("animated PNG accepted: %v", err)
	}
}

func TestF06DeclaredPixelProductOverflow(t *testing.T) {
	if _, err := checkedInspection("png", math.MaxInt, math.MaxInt, false); !errors.Is(err, imageproc.ErrSourceTooLarge) {
		t.Fatalf("overflow: %v", err)
	}
}

func TestF06AVIFIndependentPixelDecode(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	n, err := New(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	src := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			src.SetNRGBA(x, y, color.NRGBA{R: 220, G: 25, B: 15, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, src); err != nil {
		t.Fatal(err)
	}
	result, err := n.Process(context.Background(), imageproc.Source{Bytes: encoded.Bytes()}, imageproc.Transform{Width: 16, Quality: 95, Format: imageopt.FormatAVIF})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "output.avif")
	if err := os.WriteFile(file, result.Data, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(ffmpeg, "-v", "error", "-i", file, "-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "rgb24", "pipe:1")
	pixels, err := cmd.Output()
	if err != nil {
		t.Fatalf("independent AVIF decode: %v", err)
	}
	if len(pixels) != 16*16*3 {
		t.Fatalf("decoded %d bytes, want %d", len(pixels), 16*16*3)
	}
	if pixels[0] < 160 || pixels[1] > 90 || pixels[2] > 90 {
		t.Fatalf("unexpected independently decoded pixel %v", pixels[:3])
	}
}

func TestF06WebPIndependentPixelDecode(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	n, err := New(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	src := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			src.SetNRGBA(x, y, color.NRGBA{R: 220, G: 25, B: 15, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, src); err != nil {
		t.Fatal(err)
	}
	result, err := n.Process(context.Background(), imageproc.Source{Bytes: encoded.Bytes()}, imageproc.Transform{Width: 16, Quality: 95, Format: imageopt.FormatWebP})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "output.webp")
	if err := os.WriteFile(file, result.Data, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(ffmpeg, "-v", "error", "-i", file, "-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "rgb24", "pipe:1")
	pixels, err := cmd.Output()
	if err != nil {
		t.Fatalf("independent WebP decode: %v", err)
	}
	if len(pixels) != 16*16*3 {
		t.Fatalf("decoded %d bytes, want %d", len(pixels), 16*16*3)
	}
	if pixels[0] < 160 || pixels[1] > 90 || pixels[2] > 90 {
		t.Fatalf("unexpected independently decoded pixel %v", pixels[:3])
	}
}

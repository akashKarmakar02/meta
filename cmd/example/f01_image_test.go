package main

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

// imageEndpointCanProcess reports whether this build actually has a
// working image processor. Untagged builds (or tagged builds on hosts
// without the native runtime) keep the truthful reserved endpoint:
// valid → 501. Tagged builds with working encoders produce real bytes.
func imageEndpointCanProcess() bool {
	return defaultImageProcessingAvailable()
}

func TestImageEndpointInApplication(t *testing.T) {
	app := newConfiguredApp(nil, false, nil)
	validStatus := 501
	if imageEndpointCanProcess() {
		validStatus = 200
	}
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/_meta/image?src=%2Fimages%2Fhero.jpg&w=960&f=webp", validStatus},
		{"HEAD", "/_meta/image?src=%2Fimages%2Fhero.jpg&w=960&f=webp", validStatus},
		{"GET", "/_meta/image?src=%2Fetc%2Fpasswd&w=1", 400},
		{"POST", "/_meta/image", 405},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.Header.Set("X-Go-Web-Navigation", "1")
		// Native encode under -race on a cold processor can exceed the
		// default 1s test timeout; allow up to 30s.
		resp, err := app.Test(req, fiber.TestConfig{Timeout: 30 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		wantCT := "text/plain"
		if tc.status == 200 {
			wantCT = "image/"
		}
		if resp.StatusCode != tc.status || !strings.HasPrefix(resp.Header.Get("Content-Type"), wantCT) || strings.Contains(string(body), "passwd") {
			t.Fatalf("%s %s: %d %q", tc.method, tc.path, resp.StatusCode, body[:min(len(body), 64)])
		}
		// A 200 must carry real decodable WebP bytes (RIFF header at minimum).
		if tc.status == 200 && tc.method == "GET" {
			if len(body) < 4 || string(body[:4]) != "RIFF" {
				t.Fatalf("expected RIFF/WEBP body, got %d bytes", len(body))
			}
		}
	}
}

// TestImageDimsInApplication: tagged builds verify the output decodes
// at exactly the requested dimensions through the native WebP decoder.
func TestImageDimsInApplication(t *testing.T) {
	if !imageEndpointCanProcess() {
		t.Skip("native backend unavailable in this build")
	}
	app := newConfiguredApp(nil, false, nil)
	req := httptest.NewRequest("GET", "/_meta/image?src=%2Fimages%2Flandscape.jpg&w=800&f=webp", nil)
	// Native AVIF encoding is slow under -race instrumentation; the default
	// 1s app.Test timeout expires before the first encode completes.
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	dw, dh := imageDecodeDims(t, body)
	if dw != 800 || dh != 450 {
		t.Fatalf("decoded dims %dx%d want 800x450", dw, dh)
	}
}

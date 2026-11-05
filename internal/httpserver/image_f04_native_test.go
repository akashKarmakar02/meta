//go:build imageproc_native

package httpserver

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

func timeoutAfter(d time.Duration) <-chan time.Time { return time.After(d) }

// F04 tagged HTTP tests: real native processor over the real resolver,
// covering concurrency admission, mixed workloads, and cleanup under load.

// TestF04NativeConcurrentMixedWorkload: parallel valid requests across
// all output formats plus malformed sources — every valid request
// succeeds, every malformed one is refused, and the endpoint stays
// healthy after (no leaked permits, no wedged native state).
func TestF04NativeConcurrentMixedWorkload(t *testing.T) {
	app, dir := f03rNativeApp(t)
	os.WriteFile(filepath.Join(dir, "hero.jpg"), f03rGradient(t, 400, 300), 0o644)
	os.WriteFile(filepath.Join(dir, "bad.bin"), []byte("not an image"), 0o644)

	formats := []string{"webp", "avif", "jpeg", "png"}
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			f := formats[i%len(formats)]
			res, err := app.Test(httptest.NewRequest(http.MethodGet,
				fmt.Sprintf("/_meta/image?src=%%2Fimages%%2Fhero.jpg&w=100&f=%s", f), nil),
				fiber.TestConfig{Timeout: testTimeout()})
			if err != nil {
				errs <- err
				return
			}
			if res.StatusCode != 200 {
				body, _ := io.ReadAll(res.Body)
				errs <- fmt.Errorf("valid %s request: %d %s", f, res.StatusCode, body)
			}
		}(i)
	}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := app.Test(httptest.NewRequest(http.MethodGet,
				"/_meta/image?src=%2Fimages%2Fbad.bin&w=100&f=webp", nil))
			if err != nil {
				errs <- err
				return
			}
			if res.StatusCode != http.StatusUnsupportedMediaType {
				errs <- fmt.Errorf("malformed accepted: %d", res.StatusCode)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	// Endpoint healthy afterward.
	res, err := app.Test(httptest.NewRequest(http.MethodGet,
		"/_meta/image?src=%2Fimages%2Fhero.jpg&w=80&f=webp", nil),
		fiber.TestConfig{Timeout: testTimeout()})
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("endpoint unhealthy after workload: %v", res.StatusCode)
	}
}

// TestF04NativeSpecialFileOverHTTP: a FIFO source over the real pipeline
// is refused promptly, not hung.
func TestF04NativeSpecialFileOverHTTP(t *testing.T) {
	if !canMkfifo() {
		t.Skip("no fifo support")
	}
	app, dir := f03rNativeApp(t)
	if err := mkfifo(filepath.Join(dir, "pipe.jpg")); err != nil {
		t.Skipf("fifo: %v", err)
	}
	done := make(chan int, 1)
	go func() {
		res, err := app.Test(httptest.NewRequest(http.MethodGet,
			"/_meta/image?src=%2Fimages%2Fpipe.jpg&w=50&f=webp", nil))
		if err != nil {
			done <- -1
			return
		}
		done <- res.StatusCode
	}()
	select {
	case code := <-done:
		if code == 200 {
			t.Fatal("fifo processed as image")
		}
	case <-timeoutAfter(15 * time.Second):
		t.Fatal("fifo request hung")
	}
}

// TestF04NativeOutputCapReal: the default 64 MiB output cap does not
// affect legitimate work — a real AVIF encode lands far below it.
func TestF04NativeOutputCapReal(t *testing.T) {
	app, dir := f03rNativeApp(t)
	os.WriteFile(filepath.Join(dir, "big.jpg"), f03rGradient(t, 2000, 1500), 0o644)
	res, err := app.Test(httptest.NewRequest(http.MethodGet,
		"/_meta/image?src=%2Fimages%2Fbig.jpg&w=2000&f=avif", nil),
		fiber.TestConfig{Timeout: testTimeout()})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/avif" {
		t.Fatalf("large real encode: %d", res.StatusCode)
	}
	if len(body) > 64<<20 {
		t.Fatal("output exceeded cap")
	}
}

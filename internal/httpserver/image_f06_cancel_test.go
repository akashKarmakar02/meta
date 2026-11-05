package httpserver

import (
	"context"
	"fmt"
	"image"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gofiber/fiber/v3"
	"go-meta/imageopt"
	"go-meta/imageproc"
	"go-meta/internal/imagecache"
)

type cancelAfterEncode struct {
	passthrough
	cancel context.CancelFunc
}

func (p cancelAfterEncode) Process(ctx context.Context, src imageproc.Source, tr imageproc.Transform) (imageproc.Result, error) {
	out, err := p.passthrough.Process(ctx, src, tr)
	out.Data = withMagic(tr.Format, out.Data)
	p.cancel()
	return out, err
}
func TestF06CanceledEncodeDoesNotPublish(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.jpg"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	resolver, err := NewImageResolver(map[string]string{"/images/": root}, DefaultImageSourceLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer resolver.Close()
	cache, err := imagecache.Open(f05CacheCfg(t))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error { c.SetContext(ctx); return c.Next() })
	proc := cancelAfterEncode{passthrough: passthrough{caps: fullCaps()}, cancel: cancel}
	if err := MountImageProcessing(app, imageopt.DefaultLimits(), ImageEndpointConfig{Resolver: resolver, Processor: proc, Cache: cache}); err != nil {
		t.Fatal(err)
	}
	res, err := app.Test(httptest.NewRequest(http.MethodGet, "/_meta/image?src=%2Fimages%2Fa.jpg&w=20&f=webp", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusRequestTimeout {
		t.Fatalf("canceled output returned status %d", res.StatusCode)
	}
	n, _ := cache.Stats()
	if n != 0 {
		t.Fatalf("canceled output published: %d entries", n)
	}
}

type f06BlockingProcessor struct {
	passthrough
	entered chan struct{}
	release chan struct{}
}

func (p *f06BlockingProcessor) Process(ctx context.Context, src imageproc.Source, tr imageproc.Transform) (imageproc.Result, error) {
	if src.URLIdentity == "/images/slow.jpg" {
		close(p.entered)
		select {
		case <-p.release:
		case <-ctx.Done():
			return imageproc.Result{}, ctx.Err()
		}
	}
	out, err := p.passthrough.Process(ctx, src, tr)
	out.Data = withMagic(tr.Format, out.Data)
	return out, err
}
func TestF06CacheHitBypassesBusyProcessor(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"slow.jpg", "cached.jpg"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	resolver, err := NewImageResolver(map[string]string{"/images/": root}, DefaultImageSourceLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer resolver.Close()
	cache, err := imagecache.Open(f05CacheCfg(t))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	proc := &f06BlockingProcessor{passthrough: passthrough{caps: fullCaps()}, entered: make(chan struct{}), release: make(chan struct{})}
	app := fiber.New()
	if err := MountImageProcessing(app, imageopt.DefaultLimits(), ImageEndpointConfig{Resolver: resolver, Processor: proc, Cache: cache, MaxConcurrent: 1}); err != nil {
		t.Fatal(err)
	}
	path := func(name string) string { return "/_meta/image?src=%2Fimages%2F" + name + "&w=20&f=webp" }
	status, _, _ := f05Body(t, app, path("cached.jpg"))
	if status != 200 {
		t.Fatal(status)
	}
	done := make(chan error, 1)
	go func() {
		res, err := app.Test(httptest.NewRequest(http.MethodGet, path("slow.jpg"), nil))
		if err == nil {
			res.Body.Close()
		}
		done <- err
	}()
	<-proc.entered
	status, _, _ = f05Body(t, app, path("cached.jpg"))
	if status != 200 {
		t.Fatalf("cache hit while processor busy: %d", status)
	}
	close(proc.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
func TestF06ConcurrencyLimitValidated(t *testing.T) {
	root := t.TempDir()
	resolver, err := NewImageResolver(map[string]string{"/images/": root}, DefaultImageSourceLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer resolver.Close()
	err = MountImageProcessing(fiber.New(), imageopt.DefaultLimits(), ImageEndpointConfig{Resolver: resolver, Processor: passthrough{caps: fullCaps()}, MaxConcurrent: 65})
	if err == nil {
		t.Fatal("unbounded processing configuration accepted")
	}
}

type f06SnapshotProcessor struct {
	passthrough
	inspected  chan struct{}
	processing chan struct{}
	release    chan struct{}
}

func (p *f06SnapshotProcessor) DecodeConfig(imageproc.Source) (image.Config, error) {
	p.inspected <- struct{}{}
	return image.Config{Width: 1, Height: 1}, nil
}
func (p *f06SnapshotProcessor) Process(ctx context.Context, src imageproc.Source, tr imageproc.Transform) (imageproc.Result, error) {
	p.processing <- struct{}{}
	select {
	case <-p.release:
	case <-ctx.Done():
		return imageproc.Result{}, ctx.Err()
	}
	return p.passthrough.Process(ctx, src, tr)
}
func TestF06SnapshotAdmissionBoundAndCancellation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.jpg"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	resolver, err := NewImageResolver(map[string]string{"/images/": root}, DefaultImageSourceLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer resolver.Close()
	proc := &f06SnapshotProcessor{passthrough: passthrough{caps: fullCaps()}, inspected: make(chan struct{}, 3), processing: make(chan struct{}, 3), release: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		if c.Get("X-Cancel") == "yes" {
			c.SetContext(ctx)
			close(started)
		}
		return c.Next()
	})
	if err := MountImageProcessing(app, imageopt.DefaultLimits(), ImageEndpointConfig{Resolver: resolver, Processor: proc, MaxConcurrent: 1}); err != nil {
		t.Fatal(err)
	}
	path := "/_meta/image?src=%2Fimages%2Fa.jpg&w=20&f=webp"
	done := make(chan error, 3)
	send := func(mark bool) {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if mark {
			req.Header.Set("X-Cancel", "yes")
		}
		res, err := app.Test(req)
		if err == nil {
			res.Body.Close()
			if mark && res.StatusCode != http.StatusRequestTimeout {
				err = fmt.Errorf("status %d", res.StatusCode)
			}
		}
		done <- err
	}
	go send(false)
	<-proc.inspected
	<-proc.processing
	go send(false)
	<-proc.inspected
	go send(true)
	<-started
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case <-proc.inspected:
		t.Fatal("request read a source without a snapshot slot")
	default:
	}
	close(proc.release)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func TestF06HTTPFormatExclusionWithWildcard(t *testing.T) {
	app, root := f03Server(t, passthrough{caps: fullCaps()})
	if err := os.WriteFile(filepath.Join(root, "a.jpg"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/_meta/image?src=%2Fimages%2Fa.jpg&w=20&f=auto", nil)
	req.Header.Set("Accept", "image/webp;q=0,image/avif;q=0,image/jpeg;q=0,image/png;q=0,*/*;q=1")
	res, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotAcceptable || res.Header.Get("Vary") != "Accept" {
		t.Fatalf("status %d headers %v", res.StatusCode, res.Header)
	}
}

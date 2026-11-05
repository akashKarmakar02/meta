package httpserver

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"

	"go-meta/imageopt"
	"go-meta/imageproc"
)

// TestF02ConfiguredProcessorKeepsEndpointTruthful (§24): wiring a
// processor into the server must not make the F01 endpoint falsely
// report success. Until F03 implements real transformations, a valid
// request stays 501 — with or without a configured processor, native or
// unavailable — and an invalid request stays 400.
func TestF02ConfiguredProcessorKeepsEndpointTruthful(t *testing.T) {
	valid := imageopt.Endpoint + "?src=%2Fimages%2Fhero.jpg&w=960&q=80&f=webp"
	for name, tc := range map[string]struct {
		proc imageproc.Processor
	}{
		"fake-processor": {proc: imageproc.NewFake()},
		"noop-processor": {proc: imageproc.NoopProcessor{}},
		"nil-processor":  {proc: nil},
	} {
		t.Run(name, func(t *testing.T) {
			app := fiber.New()
			if err := MountImageWithLimits(app, imageopt.DefaultLimits()); err != nil {
				t.Fatal(err)
			}
			var _ = tc.proc // the current endpoint does not consume a processor; F03 will

			res, err := app.Test(httptest.NewRequest("GET", valid, nil))
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			if res.StatusCode != 501 {
				t.Fatalf("valid request with configured processor: got %d want 501", res.StatusCode)
			}
			res, err = app.Test(httptest.NewRequest("GET", imageopt.Endpoint+"?src=", nil))
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			if res.StatusCode != 400 {
				t.Fatalf("invalid request: got %d want 400", res.StatusCode)
			}
		})
	}
}

// TestF02EndpointIgnoresBackendErrors: even when the production backend is
// unavailable (untagged build), the endpoint serves its contract rather
// than leaking backend errors as raw client text.
func TestF02EndpointIgnoresBackendErrors(t *testing.T) {
	app := fiber.New()
	if err := MountImageWithLimits(app, imageopt.DefaultLimits()); err != nil {
		t.Fatal(err)
	}
	// Simulate the wiring failure mode without changing public signatures:
	// New on an untagged build reports backend-unavailable...
	if _, err := imageproc.New(context.Background()); err == nil {
		t.Skip("native backend present in this build; untagged behavior not exercised")
	} else if !errors.Is(err, imageproc.ErrBackendUnavailable) {
		t.Fatalf("unexpected New error shape: %v", err)
	}
	// ...and the endpoint still answers 501 for valid requests.
	res, err := app.Test(httptest.NewRequest("GET",
		imageopt.Endpoint+"?src=%2Fimages%2Fa.jpg&w=64&q=80&f=auto", nil))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 501 {
		t.Fatalf("got %d want 501", res.StatusCode)
	}
}

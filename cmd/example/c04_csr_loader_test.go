package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"go-meta/app/loaders"
	"go-meta/loader"
	"go-meta/routecontext"
)

// TestC04CSRLoaderDeliversDataEndToEnd drives the real app (real registry from
// loaders.Bindings, real generated routes) and proves the registered CSR loader
// runs during dispatch and its JSON result reaches both the boot shell (direct
// load) and the navigation envelope (soft navigation), for the dashboard
// [teamId] fixture route.
func TestC04CSRLoaderDeliversDataEndToEnd(t *testing.T) {
	registry, err := newLoaderRegistry(loaders.Bindings())
	if err != nil {
		t.Fatal(err)
	}
	app := newConfiguredApp(nil, false, registry)

	// Direct full-document load: the loader result is embedded as a
	// non-executable JSON script element in the boot shell.
	code, body := getBody(t, app, "dashboard shell", "/dashboard/team-42", false)
	if code != 200 {
		t.Fatalf("shell status=%d body=%q", code, body)
	}
	if !strings.Contains(body, `<script id="go-meta-csr-data" type="application/json">`) {
		t.Fatalf("shell missing loader data element: %s", body)
	}
	// The payload carries the server-defined greeting that cannot be derived
	// from the URL, plus the teamId param.
	for _, want := range []string{`"greeting":"Loaded by Go"`, `"teamId":"team-42"`, `"title":"Team team-42"`} {
		if !strings.Contains(body, want) {
			t.Errorf("shell payload missing %q in: %s", want, body)
		}
	}

	// Same declared route, different concrete URL: fresh data per request.
	code, body = getBody(t, app, "dashboard shell 99", "/dashboard/team-99", false)
	if code != 200 || !strings.Contains(body, `"teamId":"team-99"`) {
		t.Fatalf("team-99 shell status=%d body=%q", code, body)
	}

	// Navigation envelope: version 1, correct route ID, and the data field
	// carries the serialized loader result.
	code, body = getBody(t, app, "dashboard envelope", "/dashboard/team-42", true)
	if code != 200 {
		t.Fatalf("envelope status=%d body=%q", code, body)
	}
	var env struct {
		Version     int             `json:"version"`
		RouteID     string          `json:"routeId"`
		Mode        string          `json:"mode"`
		ClientEntry *string         `json:"clientEntry"`
		Data        json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("envelope not JSON: %v in %q", err, body)
	}
	if env.Version != 1 || env.RouteID != "dashboard/[teamId]/page.svelte" || env.Mode != "csr" {
		t.Fatalf("envelope version=%d routeId=%q mode=%q", env.Version, env.RouteID, env.Mode)
	}
	if env.ClientEntry == nil || *env.ClientEntry != "dashboard/[teamId]/page.svelte" {
		t.Fatalf("clientEntry=%v", env.ClientEntry)
	}
	if len(env.Data) == 0 {
		t.Fatalf("envelope missing data field: %s", body)
	}
	var payload map[string]any
	if err := json.Unmarshal(env.Data, &payload); err != nil {
		t.Fatalf("data not a JSON object: %v in %q", err, string(env.Data))
	}
	if payload["greeting"] != "Loaded by Go" || payload["teamId"] != "team-42" {
		t.Fatalf("data payload=%v", payload)
	}
}

// TestC04CSRRouteWithoutLoaderHasNoData proves a CSR route with no registered
// loader keeps the existing shell and envelope with no data field (absent, not
// null).
func TestC04CSRRouteWithoutLoaderHasNoData(t *testing.T) {
	registry, err := newLoaderRegistry(loaders.Bindings())
	if err != nil {
		t.Fatal(err)
	}
	app := newConfiguredApp(nil, false, registry)

	// products/[id] has no loader binding.
	code, body := getBody(t, app, "products shell", "/products/7", false)
	if code != 200 {
		t.Fatalf("shell status=%d", code)
	}
	if strings.Contains(body, `id="go-meta-csr-data"`) {
		t.Fatalf("no-loader shell must not embed a data element: %s", body)
	}

	code, body = getBody(t, app, "products envelope", "/products/7", true)
	if code != 200 {
		t.Fatalf("envelope status=%d", code)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatalf("envelope not JSON: %v", err)
	}
	if _, present := raw["data"]; present {
		t.Fatalf("no-loader envelope must omit the data field, got: %s", body)
	}
}

// TestC04CSRLoaderExecutesExactlyOncePerRequest proves the loader runs once per
// direct request and once per navigation request, never twice during bootstrap.
func TestC04CSRLoaderExecutesExactlyOncePerRequest(t *testing.T) {
	var calls atomic.Int32
	registry, err := newLoaderRegistry([]loader.Binding{
		loader.Bind("dashboard/[teamId]/page.svelte", func(context.Context, routecontext.Route) (map[string]string, error) {
			calls.Add(1)
			return map[string]string{"ok": "1"}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	app := newConfiguredApp(nil, false, registry)

	getBody(t, app, "direct", "/dashboard/t1", false)
	if calls.Load() != 1 {
		t.Fatalf("direct load ran loader %d times, want 1", calls.Load())
	}
	getBody(t, app, "nav", "/dashboard/t1", true)
	if calls.Load() != 2 {
		t.Fatalf("after navigation ran loader %d times, want 2", calls.Load())
	}
}

// TestC04CSRLoaderReceivesParamsAndQuery proves the loader gets the validated
// route snapshot (path params + query) for a dynamic CSR route.
func TestC04CSRLoaderReceivesParamsAndQuery(t *testing.T) {
	var mu sync.Mutex
	var gotTeam, gotQuery string
	registry, err := newLoaderRegistry([]loader.Binding{
		loader.Bind("dashboard/[teamId]/page.svelte", func(_ context.Context, r routecontext.Route) (map[string]string, error) {
			team, _ := r.Params.Get("teamId")
			q := r.Query.Get("tab")
			mu.Lock()
			gotTeam, gotQuery = team, q
			mu.Unlock()
			return map[string]string{"team": team}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	app := newConfiguredApp(nil, false, registry)
	if code, _ := getBody(t, app, "dash", "/dashboard/team-7?tab=overview", false); code != 200 {
		t.Fatalf("status=%d", code)
	}
	mu.Lock()
	defer mu.Unlock()
	if gotTeam != "team-7" || gotQuery != "overview" {
		t.Fatalf("loader saw team=%q query=%q", gotTeam, gotQuery)
	}
}

// TestC04CSRLoaderFailureStopsPublication proves a loader error prevents a
// successful CSR response: a 5xx, no shell/envelope with missing data, and no
// raw error text leaked. C05 maps a generic (unclassified) loader error to
// 500; the deadline/cancellation cases are covered by the C05 tests.
func TestC04CSRLoaderFailureStopsPublication(t *testing.T) {
	registry, err := newLoaderRegistry([]loader.Binding{
		loader.Bind("dashboard/[teamId]/page.svelte", func(context.Context, routecontext.Route) (map[string]string, error) {
			return nil, errors.New("upstream timeout: internal detail")
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	app := newConfiguredApp(nil, false, registry)
	code, body := getBody(t, app, "failing", "/dashboard/team-1", false)
	if code != 500 {
		t.Fatalf("status=%d, want 500", code)
	}
	if strings.Contains(body, "upstream timeout") || strings.Contains(body, "internal detail") {
		t.Fatalf("raw loader error leaked: %s", body)
	}
}

// TestC04CSRSerializationFailureStopsPublication proves an unsupported result
// type (a function) prevents a successful CSR response rather than publishing a
// page with missing data.
func TestC04CSRSerializationFailureStopsPublication(t *testing.T) {
	registry, err := newLoaderRegistry([]loader.Binding{
		loader.Bind("dashboard/[teamId]/page.svelte", func(context.Context, routecontext.Route) (func(), error) {
			return func() {}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	app := newConfiguredApp(nil, false, registry)
	code, _ := getBody(t, app, "unserializable", "/dashboard/team-1", false)
	if code != 500 {
		t.Fatalf("status=%d, want 500 (serialization failure must stop publication)", code)
	}
}

// TestC04CSRConcurrentRequestsAreIsolated proves concurrent CSR requests each
// run the loader once with isolated results (no cross-request data leak).
func TestC04CSRConcurrentRequestsAreIsolated(t *testing.T) {
	var calls atomic.Int32
	registry, err := newLoaderRegistry([]loader.Binding{
		loader.Bind("dashboard/[teamId]/page.svelte", func(_ context.Context, r routecontext.Route) (map[string]string, error) {
			calls.Add(1)
			team, _ := r.Params.Get("teamId")
			return map[string]string{"team": team}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	app := newConfiguredApp(nil, false, registry)

	const n = 30
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			team := "team-" + string(rune('a'+i%26))
			req := httptest.NewRequest("GET", "/dashboard/"+team, nil)
			res, err := app.Test(req)
			if err != nil {
				errs <- err
				return
			}
			defer res.Body.Close()
			body, _ := io.ReadAll(res.Body)
			if res.StatusCode != 200 {
				errs <- errStatus{res.StatusCode}
				return
			}
			if !strings.Contains(string(body), `"team":"`+team+`"`) {
				errs <- errLeak{team, string(body)}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	if got := calls.Load(); got != n {
		t.Fatalf("concurrent: loader ran %d times, want %d", got, n)
	}
}

// TestC04SSRLoadersUnchanged is a regression guard: SSR loaders still render
// typed data into templ and do NOT get a JSON data field in the envelope.
func TestC04SSRLoadersUnchanged(t *testing.T) {
	registry, err := newLoaderRegistry(loaders.Bindings())
	if err != nil {
		t.Fatal(err)
	}
	app := newConfiguredApp(nil, false, registry)

	// SSR posts route renders typed data into HTML.
	code, body := getBody(t, app, "posts", "/posts/42", false)
	if code != 200 || !strings.Contains(body, "Example Post") {
		t.Fatalf("SSR status=%d body=%q", code, body)
	}
	// SSR envelope must not carry a JSON data field (C03 typed context delivery).
	code, body = getBody(t, app, "posts env", "/posts/42", true)
	if code != 200 {
		t.Fatalf("SSR envelope status=%d", code)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatalf("SSR envelope not JSON: %v", err)
	}
	if _, present := raw["data"]; present {
		t.Fatalf("SSR envelope must not carry a data field: %s", body)
	}
}

// TestC04EscapedPathRunsNoLoader proves an invalid escaped path is rejected
// before any loader runs.
func TestC04EscapedPathRunsNoLoader(t *testing.T) {
	var calls atomic.Int32
	registry, err := newLoaderRegistry([]loader.Binding{
		loader.Bind("dashboard/[teamId]/page.svelte", func(context.Context, routecontext.Route) (map[string]string, error) {
			calls.Add(1)
			return map[string]string{}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	app := newConfiguredApp(nil, false, registry)
	if code, _ := getBody(t, app, "invalid", "/%2e%2e/secret", false); code != 400 {
		t.Fatalf("invalid path status=%d, want 400", code)
	}
	if calls.Load() != 0 {
		t.Fatalf("loader ran %d times on invalid path, want 0", calls.Load())
	}
}

// errStatus and errLeak are small error carriers for the concurrency test.
type errStatus struct{ code int }

func (e errStatus) Error() string { return "unexpected status " + strconv.Itoa(e.code) }

type errLeak struct{ team, body string }

func (e errLeak) Error() string { return "data leak for " + e.team + ": " + e.body }

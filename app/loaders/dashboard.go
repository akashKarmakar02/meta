package loaders

import (
	"context"
	"fmt"

	"go-meta/routecontext"
)

// DashboardData is the demonstration loader result for the C04 CSR example.
// It is a demonstration fixture, not real business data.
//
// Greeting is a fixed server-defined string that cannot be derived from the
// request URL alone. A page that displays it proves the value came from the
// Go loader, not from window.location.
type DashboardData struct {
	TeamID   string `json:"teamId"`
	Title    string `json:"title"`
	Greeting string `json:"greeting"`
}

// LoadDashboard is a demonstration CSR loader bound to the
// dashboard/[teamId] fixture route. It reads the teamId path parameter through
// the B05 route snapshot and returns a typed struct. The result is
// JSON-serialized by the framework and delivered to the Svelte page as a prop
// (C04) — the page never re-derives it from the URL.
func LoadDashboard(_ context.Context, route routecontext.Route) (DashboardData, error) {
	teamID, ok := route.Params.Get("teamId")
	if !ok {
		// The route pattern guarantees teamId is present; reaching here means
		// the snapshot is malformed. Surface it as a loader error (C04 turns
		// this into a 500 before the page renders; full status mapping is C05).
		return DashboardData{}, fmt.Errorf("teamId parameter missing")
	}
	return DashboardData{
		TeamID:   teamID,
		Title:    "Team " + teamID,
		Greeting: "Loaded by Go",
	}, nil
}

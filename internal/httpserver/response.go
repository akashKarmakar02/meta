package httpserver

import "encoding/json"

// NavigationVersion is the soft-navigation protocol version this build
// speaks. Unsupported versions trigger a normal full page navigation on
// the client (blueprint 5.1).
const NavigationVersion = 1

// NavigationHeader marks a GET as a soft-navigation request expecting the
// JSON envelope instead of a full document.
const NavigationHeader = "X-Go-Web-Navigation"

// NavigationResponse is the JSON envelope for soft navigation, mirroring
// frontend/types.ts field-for-field. Both sides are pinned to the same
// checked-in fixtures (tests/fixtures) so the contract cannot drift.
//
// HTML carries server-owned templ output only. ClientEntry maps to the
// generated static import registry; it is never evaluated as a path.
type NavigationResponse struct {
	Version     int      `json:"version"`
	URL         string   `json:"url"`
	RouteID     string   `json:"routeId"`
	Mode        string   `json:"mode"` // "ssr" | "csr"
	Title       string   `json:"title"`
	Layouts     []string `json:"layouts"`
	HTML        string   `json:"html"`
	ClientEntry *string  `json:"clientEntry"`
}

// MarshalNavigation serializes the envelope with the exact wire shape
// pinned by tests/fixtures.
func MarshalNavigation(resp NavigationResponse) ([]byte, error) {
	return json.Marshal(resp)
}

// UnmarshalNavigation parses an envelope; callers must check Version.
func UnmarshalNavigation(data []byte) (NavigationResponse, error) {
	var resp NavigationResponse
	err := json.Unmarshal(data, &resp)
	return resp, err
}

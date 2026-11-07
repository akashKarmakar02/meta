package httpserver

import (
	"encoding/json"
	"github.com/akashKarmakar02/meta/internal/metastate"
)

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
// HTML carries server-owned templ output only, including the visual layout
// and (for CSR under templ) the page mount. ClientEntry maps to the
// generated static import registry; it is never evaluated as a path.
//
// Data is the optional C04 CSR loader payload. It is json.RawMessage so the
// exact JSON bytes produced by the server are preserved verbatim (including a
// legitimate JSON null), and omitempty so routes without a loader omit the
// field entirely — letting the client distinguish "no loader" from "loader
// returned null". SSR envelopes leave Data nil, so the field is absent and
// existing SSR behavior is unchanged.
type NavigationResponse struct {
	Version     int             `json:"version"`
	URL         string          `json:"url"`
	RouteID     string          `json:"routeId"`
	Mode        string          `json:"mode"` // "ssr" | "csr"
	Title       string          `json:"title"`
	Layouts     []string        `json:"layouts"`
	HTML        string          `json:"html"`
	ClientEntry *string         `json:"clientEntry"`
	Data        json.RawMessage `json:"data,omitempty"`
	Metadata    *metastate.Page `json:"metadata,omitempty"`
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

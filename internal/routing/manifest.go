// manifest.go — internal route manifest schema, independent of Fiber and
// of any HTTP concern. The same JSON contract is consumed by the Go code
// generator (C02+) and by the browser toolchain: generated code and Vite
// share one manifest.

package routing

// ManifestVersion is the schema version of the route manifest. Consumers
// must reject manifests with an unknown version rather than guessing.
const ManifestVersion = 1

// Manifest is the serialized form of a discovered route set. It reuses the
// Route and Layout descriptors from schema.go — there is no second route
// model.
type Manifest struct {
	// Version is ManifestVersion.
	Version int `json:"version"`
	// Routes is the full route set, ordered by normalized path (the
	// discovery ordering), layouts outermost -> innermost.
	Routes []Route `json:"routes"`
}

// NewManifest builds a manifest from discovered routes. Input is expected
// to come from Discover (already sorted, validated); this constructor only
// stamps the version.
func NewManifest(routes []Route) Manifest {
	if routes == nil {
		routes = []Route{}
	}
	return Manifest{Version: ManifestVersion, Routes: routes}
}

// Package httpserver owns server-side wiring for client assets: reading
// the Vite production manifest and resolving the URLs the shell injects.
// The manifest is read once at startup, never per request; failures fail
// startup explicitly instead of serving nonfunctional CSR pages.
package httpserver

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// viteManifestPath is the manifest location inside the Vite outDir.
const viteManifestPath = ".vite/manifest.json"

// viteEntryKey identifies the client entry in the Vite manifest.
const viteEntryKey = "frontend/entry.ts"

// GlobalStylesheetKey is the optional independent CSS entry used by starters.
const GlobalStylesheetKey = "frontend/app.css"

// Assets carries resolved production client asset URLs. Hashed filenames
// come only from the manifest — nothing hardcodes them.
type Assets struct {
	// DevClientURL is set only for a local Vite development server.
	DevClientURL string
	// EntryScriptURL is the module script URL for the client entry,
	// e.g. /assets/entry-<hash>.js.
	EntryScriptURL string
	// EntryCSSURLs are stylesheet URLs for the entry, in manifest order.
	EntryCSSURLs []string
}

// DevelopmentAssets constructs script URLs for the explicitly configured
// local Vite origin. Production never consults the development server.
func DevelopmentAssets(origin string, globalStylesheet ...bool) (Assets, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return Assets{}, fmt.Errorf("invalid Vite development origin %q", origin)
	}
	host, port, err := net.SplitHostPort(u.Host)
	portNumber, portErr := strconv.Atoi(port)
	if err != nil || host != "127.0.0.1" || portErr != nil || portNumber < 1 || portNumber > 65535 {
		return Assets{}, fmt.Errorf("Vite development origin must use 127.0.0.1 and an explicit port")
	}
	assets := Assets{DevClientURL: "/_go_meta_vite/@vite/client", EntryScriptURL: "/_go_meta_vite/" + viteEntryKey}
	if len(globalStylesheet) > 0 && globalStylesheet[0] {
		info, err := os.Stat(GlobalStylesheetKey)
		if err != nil || !info.Mode().IsRegular() {
			return Assets{}, fmt.Errorf("development stylesheet %s must exist as a regular file", GlobalStylesheetKey)
		}
		assets.EntryCSSURLs = append(assets.EntryCSSURLs, "/_go_meta_vite/"+GlobalStylesheetKey+"?direct")
	}
	return assets, nil
}

// viteManifestChunk mirrors the fields go-meta consumes from Vite's
// build manifest. Unknown fields are ignored.
type viteManifestChunk struct {
	File    string   `json:"file"`
	Src     string   `json:"src"`
	IsEntry bool     `json:"isEntry"`
	CSS     []string `json:"css"`
}

// LoadAssets reads clientDir/.vite/manifest.json and resolves the entry
// assets. It fails explicitly on a missing manifest, a missing or
// non-entry key, unsafe asset paths, or files absent from disk — callers
// treat an error as fatal rather than serving a shell that cannot boot.
//
// clientDir is interpreted relative to the process working directory;
// the example server assumes the repository root (blueprint §6 accepts
// `go run ./cmd/example` from the repo root in Phase 1).
func LoadAssets(clientDir string, globalStylesheet ...bool) (Assets, error) {
	manifestPath := filepath.Join(clientDir, ".vite", "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return Assets{}, fmt.Errorf("read vite manifest %s: %w", manifestPath, err)
	}

	var manifest map[string]viteManifestChunk
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Assets{}, fmt.Errorf("parse vite manifest %s: %w", manifestPath, err)
	}

	entry, ok := manifest[viteEntryKey]
	if !ok {
		return Assets{}, fmt.Errorf("vite manifest %s: entry %s not found; run the client build", manifestPath, viteEntryKey)
	}
	if !entry.IsEntry {
		return Assets{}, fmt.Errorf("vite manifest %s: %s is not marked isEntry", manifestPath, viteEntryKey)
	}

	scriptURL, err := resolveAssetURL(clientDir, entry.File)
	if err != nil {
		return Assets{}, fmt.Errorf("vite manifest %s: entry script: %w", manifestPath, err)
	}

	assets := Assets{EntryScriptURL: scriptURL}
	for _, css := range entry.CSS {
		cssURL, err := resolveAssetURL(clientDir, css)
		if err != nil {
			return Assets{}, fmt.Errorf("vite manifest %s: entry css: %w", manifestPath, err)
		}
		assets.EntryCSSURLs = append(assets.EntryCSSURLs, cssURL)
	}
	if len(globalStylesheet) > 0 && globalStylesheet[0] {
		stylesheet, ok := manifest[GlobalStylesheetKey]
		if !ok {
			return Assets{}, fmt.Errorf("client manifest missing stylesheet %s", GlobalStylesheetKey)
		}
		if !strings.HasSuffix(stylesheet.File, ".css") {
			return Assets{}, fmt.Errorf("client stylesheet %s is not a CSS asset", GlobalStylesheetKey)
		}
		cssURL, err := resolveAssetURL(clientDir, stylesheet.File)
		if err != nil {
			return Assets{}, fmt.Errorf("client stylesheet: %w", err)
		}
		found := false
		for _, existing := range assets.EntryCSSURLs {
			if existing == cssURL {
				found = true
				break
			}
		}
		if !found {
			assets.EntryCSSURLs = append([]string{cssURL}, assets.EntryCSSURLs...)
		}
	}
	return assets, nil
}

// resolveAssetURL validates a manifest asset path and returns its serving
// URL. Paths are forward-slash relative references into the client output
// (e.g. assets/entry-<hash>.js); anything that could escape the asset
// root — absolute paths, parent segments, backslashes, control
// characters — is rejected so a tampered manifest cannot inject URLs
// outside the served prefix. The referenced file must exist on disk so a
// stale manifest fails at startup, not in the browser.
func resolveAssetURL(clientDir, file string) (string, error) {
	if file == "" {
		return "", fmt.Errorf("empty asset path")
	}
	if strings.HasPrefix(file, "/") || filepath.IsAbs(file) {
		return "", fmt.Errorf("asset path %q must be relative", file)
	}
	for _, seg := range strings.Split(file, "/") {
		if seg == ".." {
			return "", fmt.Errorf("asset path %q must not contain parent segments", file)
		}
	}
	if strings.ContainsAny(file, "\\\r\n\t") {
		return "", fmt.Errorf("asset path %q contains unsafe characters", file)
	}
	for i := 0; i < len(file); i++ {
		if file[i] < 0x20 {
			return "", fmt.Errorf("asset path %q contains control characters", file)
		}
	}
	if _, err := os.Stat(filepath.Join(clientDir, filepath.FromSlash(file))); err != nil {
		return "", fmt.Errorf("asset %s missing from disk: %w", file, err)
	}
	return "/" + file, nil
}

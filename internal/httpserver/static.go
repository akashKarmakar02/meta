package httpserver

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/static"
)

// assetMaxAge is the Cache-Control max-age for served production assets.
// Every filename under dist/client/assets is content-hashed by Vite, so a
// URL names exactly one immutable byte sequence; caching for a year is
// safe and standard for that layout.
const assetMaxAge = 31536000

// MountAssets registers GET /assets/* on app, serving the hashed
// production client bundle from clientDir/assets — the same prefix E08's
// shell injects (EntryScriptURL is /assets/<file>). Only the assets
// directory is exposed: the Vite manifest (clientDir/.vite/) and anything
// outside clientDir/assets are unreachable through this route by
// construction, and traversal attempts are rejected by the static
// middleware's path sanitization and return 404.
//
// Missing assets return a true 404 via the app's error handler — never a
// CSR shell. The assets directory must exist at startup; a deployment
// without built assets fails explicitly here rather than serving shells
// that cannot boot (E08/E09 contract).
func MountAssets(app *fiber.App, clientDir string) error {
	assetsDir := filepath.Join(clientDir, "assets")
	info, err := os.Stat(assetsDir)
	if err != nil {
		return fmt.Errorf("httpserver: client assets directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("httpserver: %s is not a directory", assetsDir)
	}

	app.Get("/assets/*", static.New(assetsDir, static.Config{
		MaxAge: assetMaxAge,
		NotFoundHandler: func(c fiber.Ctx) error {
			// Route through the app's error handler for a real 404 status;
			// without this the middleware resets to 200 and defers to the
			// next route, which risks falling into a shell handler.
			return fiber.ErrNotFound
		},
	}))
	return nil
}

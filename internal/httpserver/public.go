package httpserver

import (
	"fmt"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/gofiber/fiber/v3"
)

// MountPublic serves files beneath dir at root-relative URLs. Missing files
// fall through to page routing. Directories and hidden paths, except
// .well-known, are not exposed. An absent directory is allowed so developers
// can add public files after starting their application.
func MountPublic(app *fiber.App, dir string) error {
	if dir == "" {
		return nil
	}
	info, err := os.Stat(dir)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("public directory: %w", err)
	}
	if err == nil && !info.IsDir() {
		return fmt.Errorf("public directory %q is not a directory", dir)
	}
	app.Use(func(c fiber.Ctx) error {
		if c.Method() != fiber.MethodGet && c.Method() != fiber.MethodHead {
			return c.Next()
		}
		raw := string(c.Request().URI().PathOriginal())
		if !safePagePath(raw) {
			return fiber.NewError(fiber.StatusBadRequest, "invalid URL path")
		}
		parts := strings.Split(strings.TrimPrefix(raw, "/"), "/")
		for i, part := range parts {
			decoded, err := url.PathUnescape(part)
			if err != nil || decoded == "" || (strings.HasPrefix(decoded, ".") && !(i == 0 && decoded == ".well-known")) {
				return c.Next()
			}
			parts[i] = decoded
		}
		if len(parts) == 0 {
			return c.Next()
		}
		switch parts[0] {
		case "_meta", "_go_meta_vite", "assets", "healthz":
			return c.Next()
		}
		root, err := os.OpenRoot(dir)
		if err != nil {
			return c.Next()
		}
		// Refuse symlinks and special files before opening them. In addition,
		// os.Root confines resolution if the directory changes concurrently.
		for i := range parts {
			info, statErr := root.Lstat(filepath.Join(parts[:i+1]...))
			if statErr != nil || info.Mode()&os.ModeSymlink != 0 ||
				(i < len(parts)-1 && !info.IsDir()) ||
				(i == len(parts)-1 && !info.Mode().IsRegular()) {
				_ = root.Close()
				return c.Next()
			}
		}
		file, err := root.Open(filepath.Join(parts...))
		closeErr := root.Close()
		if err != nil {
			return c.Next()
		}
		if closeErr != nil {
			_ = file.Close()
			return fiber.ErrInternalServerError
		}
		stat, err := file.Stat()
		if err != nil || !stat.Mode().IsRegular() {
			_ = file.Close()
			return c.Next()
		}
		contentType := mime.TypeByExtension(filepath.Ext(stat.Name()))
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		c.Set(fiber.HeaderContentType, contentType)
		c.Set(fiber.HeaderCacheControl, "no-cache")
		c.Set("X-Content-Type-Options", "nosniff")
		if c.Method() == fiber.MethodHead {
			c.Set(fiber.HeaderContentLength, fmt.Sprint(stat.Size()))
			return file.Close()
		}
		// fasthttp closes streams implementing io.Closer after the response is sent.
		return c.SendStream(file)
	})
	return nil
}

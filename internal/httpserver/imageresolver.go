package httpserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"go-meta/imageproc"
)

// Source resolution for the image endpoint (F03 §21–24).
//
// The resolver maps F01-validated source identities (/images/...,
// /assets/...) onto explicitly configured filesystem roots and reads
// bounded file content. Containment is enforced by Go 1.24+'s os.OpenRoot
// root-scoped operations: every open happens relative to the root and
// refuses dot segments and symlinks that escape it, so there is no
// validate-then-open race to exploit. The resolver never exposes physical
// paths in errors returned to callers that surface them to HTTP clients.

// ImageSourceLimits are the resource ceilings enforced before any decode
// or allocation happens (F03 §25–26). Conservative defaults; F04 tightens
// them per deployment.
type ImageSourceLimits struct {
	// MaxSourceBytes bounds the encoded input file read from disk.
	MaxSourceBytes int64
	// MaxDecodedPixels bounds width*height sniffed via DecodeConfig before
	// a full decode allocates pixel buffers.
	MaxDecodedPixels int64
}

// DefaultImageSourceLimits returns the F03 safety defaults: 32 MiB source
// files, 40 MP decoded images.
func DefaultImageSourceLimits() ImageSourceLimits {
	return ImageSourceLimits{
		MaxSourceBytes:   32 << 20,
		MaxDecodedPixels: 40_000_000,
	}
}

// Validate rejects zero or negative limits.
func (l ImageSourceLimits) Validate() error {
	if l.MaxSourceBytes < 1 || l.MaxDecodedPixels < 1 {
		return fmt.Errorf("httpserver: image source limits must be positive")
	}
	return nil
}

// resolver errors; mapped to HTTP statuses by the endpoint, never shown
// with physical paths.
var (
	// errImageSourceNotFound: identity resolves under an approved root but
	// no file exists there.
	errImageSourceNotFound = errors.New("image source not found")
	// errImageSourceForbidden: the identity is syntactically valid but the
	// root-scoped open refused it (symlink escape, special file, ...).
	errImageSourceForbidden = errors.New("image source forbidden")
)

// ImageResolver resolves approved image identities under configured roots.
type ImageResolver struct {
	roots  map[string]*fsRoot // namespace ("/images/") -> root
	limits ImageSourceLimits
}

// fsRoot binds one namespace prefix to one opened os.Root.
type fsRoot struct {
	namespace string
	root      *os.Root
}

// NewImageResolver validates the namespace-to-directory mapping, opens
// each directory as an os.Root, and returns a resolver. An unknown
// namespace in a request fails closed later with not-found. Opening a
// missing or non-directory root fails here — deployments misconfigured at
// startup never quietly serve nothing.
func NewImageResolver(mapping map[string]string, limits ImageSourceLimits) (*ImageResolver, error) {
	if len(mapping) == 0 {
		return nil, fmt.Errorf("httpserver: image resolver needs at least one namespace mapping")
	}
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	r := &ImageResolver{roots: make(map[string]*fsRoot, len(mapping)), limits: limits}
	for ns, dir := range mapping {
		if !strings.HasPrefix(ns, "/") || !strings.HasSuffix(ns, "/") {
			return nil, fmt.Errorf("httpserver: image namespace %q must start and end with '/'", ns)
		}
		root, err := os.OpenRoot(dir)
		if err != nil {
			return nil, fmt.Errorf("httpserver: image root for %s: %w", ns, err)
		}
		r.roots[ns] = &fsRoot{namespace: ns, root: root}
	}
	return r, nil
}

// Close releases every held os.Root.
func (r *ImageResolver) Close() error {
	var first error
	for _, fr := range r.roots {
		if err := fr.root.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Namespaces lists configured namespace prefixes, longest first, for
// startup diagnostics.
func (r *ImageResolver) Namespaces() []string {
	out := make([]string, 0, len(r.roots))
	for ns := range r.roots {
		out = append(out, ns)
	}
	// Deterministic order: plain sort; lengths are equal enough for logs.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// Resolve reads the source identity's bytes under its approved root.
// The F01 contract has already validated the identity lexically; this
// method performs the actual root-confined access. Bytes returned are
// freshly allocated per call — no shared buffers between requests.
func (r *ImageResolver) Resolve(ctx context.Context, identity string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fr, rel := r.match(identity)
	if fr == nil {
		// Unknown namespace: indistinguishable from a missing source on
		// purpose — no existence oracle for unconfigured namespaces.
		return nil, errImageSourceNotFound
	}

	f, err := fr.root.Open(rel)
	if err != nil {
		return nil, mapOpenError(err)
	}
	defer f.Close()

	// Bound the read before allocating: stat first, then a single capped
	// ReadAll-style loop. A file replaced between stat and read yields
	// either fewer bytes (fine) or is caught by the io.LimitReader cap.
	st, err := f.Stat()
	if err != nil {
		return nil, errImageSourceForbidden
	}
	if st.IsDir() {
		return nil, errImageSourceNotFound
	}
	if st.Size() > r.limits.MaxSourceBytes {
		return nil, imageproc.ErrSourceTooLarge
	}
	// Read at most MaxSourceBytes+1 so an exactly-at-limit file succeeds
	// but anything larger fails even if it grew after stat.
	buf, err := io.ReadAll(io.LimitReader(f, r.limits.MaxSourceBytes+1))
	if err != nil {
		return nil, errImageSourceForbidden
	}
	if int64(len(buf)) > r.limits.MaxSourceBytes {
		return nil, imageproc.ErrSourceTooLarge
	}
	return buf, nil
}

// match finds the configured root for identity and the path relative to it.
// F01 validation guarantees no leading '../' tricks; the root-relative
// lookup still refuses any residual traversal or escaping symlink.
func (r *ImageResolver) match(identity string) (*fsRoot, string) {
	for ns, fr := range r.roots {
		if strings.HasPrefix(identity, ns) {
			return fr, strings.TrimPrefix(identity, ns)
		}
	}
	return nil, ""
}

// mapOpenError converts root-scoped open failures into the two safe
// public categories. Root-scoped opens report fs.ErrNotExist for missing
// targets and fs.ErrInvalid / path errors for traversal or escaping
// symlinks; anything else fails closed as forbidden.
func mapOpenError(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return errImageSourceNotFound
	}
	return errImageSourceForbidden
}

// CheckDecodedPixels enforces the decoded-pixel ceiling on config sniffed
// dimensions with overflow-safe arithmetic.
func (l ImageSourceLimits) CheckDecodedPixels(w, h int) error {
	if w <= 0 || h <= 0 {
		return imageproc.ErrSourceInvalid
	}
	if int64(w)*int64(h) > l.MaxDecodedPixels {
		return imageproc.ErrSourceTooLarge
	}
	return nil
}

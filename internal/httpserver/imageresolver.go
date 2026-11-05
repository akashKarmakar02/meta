package httpserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"sort"
	"strings"

	"go-meta/imageproc"
)

// openNonblockFlags is the platform-specific nonblocking-open flag used
// when opening sources so special files cannot stall the request. On
// platforms without it the flag is zero and the regular-file check still
// rejects non-regular sources after a (potentially blocking) open — the
// flag is an availability hardening, not the containment boundary.
const openNonblockFlags = syscallO_NONBLOCK

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
	if l.MaxSourceBytes < 1 || l.MaxSourceBytes == math.MaxInt64 || l.MaxDecodedPixels < 1 {
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
			_ = r.Close()
			return nil, fmt.Errorf("httpserver: image namespace %q must start and end with '/'", ns)
		}
		root, err := os.OpenRoot(dir)
		if err != nil {
			_ = r.Close()
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
	sort.Slice(out, func(i, j int) bool {
		if len(out[i]) != len(out[j]) {
			return len(out[i]) > len(out[j])
		}
		return out[i] < out[j]
	})
	return out
}

// Resolve reads the source identity's bytes under its approved root.
// The F01 contract has already validated the identity lexically; this
// method performs the actual root-confined access. Bytes returned are
// freshly allocated per call — no shared buffers between requests. The
// whole read is one descriptor's snapshot: open (nonblocking, so a FIFO
// cannot stall it), verify a regular file via the same descriptor's
// fstat, then bounded-read from that descriptor. Inspection and decoding
// upstream operate exclusively on these returned bytes — the resolver
// never reopens a pathname between safety checks, so a source replaced
// mid-request cannot smuggle different content past a prior validation.
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

	// Open with O_NONBLOCK so an attacker-placed FIFO or device node
	// cannot block the worker forever; os.Root confines the open to the
	// root regardless of flags. Directories still open fine and are
	// rejected by the mode check below.
	f, err := fr.root.OpenFile(rel, os.O_RDONLY|openNonblockFlags, 0)
	if err != nil {
		return nil, mapOpenError(err)
	}
	defer f.Close()

	// Only regular files are sources. Sockets, FIFOs, devices, and other
	// special files fail closed as forbidden — before any read can block
	// or return attacker-controlled stream content.
	st, err := f.Stat()
	if err != nil {
		return nil, errImageSourceForbidden
	}
	if !st.Mode().IsRegular() {
		if st.IsDir() {
			return nil, errImageSourceNotFound
		}
		return nil, errImageSourceForbidden
	}

	// Bounded read of at most MaxSourceBytes+1 bytes: an exactly-at-limit
	// file succeeds, anything larger fails even if it grew after open.
	// The limit applies to bytes actually read, never to metadata size,
	// so growing and shrinking files are both handled by the cap itself.
	buf, err := io.ReadAll(io.LimitReader(f, r.limits.MaxSourceBytes+1))
	if err != nil {
		return nil, errImageSourceForbidden
	}
	if int64(len(buf)) > r.limits.MaxSourceBytes {
		return nil, imageproc.ErrSourceTooLarge
	}
	return buf, nil
}

// namespaceFor returns the configured namespace prefix that owns identity,
// or "" when no root is configured for it (the request fails resolution
// before this matters; used by the F05 cache identity, where a stable
// namespace distinguishes identical relative paths under different roots).
func (r *ImageResolver) namespaceFor(identity string) string {
	fr, _ := r.match(identity)
	if fr == nil {
		return ""
	}
	return fr.namespace
}

// match finds the configured root for identity and the path relative to it.
// F01 validation guarantees no leading '../' tricks; the root-relative
// lookup still refuses any residual traversal or escaping symlink.
func (r *ImageResolver) match(identity string) (*fsRoot, string) {
	var selected *fsRoot
	longest := 0
	for ns, fr := range r.roots {
		if len(ns) > longest && strings.HasPrefix(identity, ns) {
			selected, longest = fr, len(ns)
		}
	}
	if selected == nil {
		return nil, ""
	}
	return selected, strings.TrimPrefix(identity, selected.namespace)
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

// CheckDecodedPixels enforces the dimension and decoded-pixel ceilings on
// header-inspected dimensions with overflow-safe arithmetic. Both axes
// must be positive; the total must stay within MaxDecodedPixels. Called
// before any full-frame decode.
func (l ImageSourceLimits) CheckDecodedPixels(w, h int) error {
	if w <= 0 || h <= 0 {
		return imageproc.ErrSourceInvalid
	}
	if int64(w) > l.MaxDecodedPixels/int64(h) {
		return imageproc.ErrSourceTooLarge
	}
	return nil
}

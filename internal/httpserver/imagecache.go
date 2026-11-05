package httpserver

import (
	"crypto/sha256"
	"encoding/hex"

	"go-meta/imageproc"
)

// F05 disk-cache integration at the HTTP endpoint. The cache sits between
// source resolution/versioning and image processing:
//
//	validate request → negotiate format → resolve bounded source snapshot
//	→ SHA-256 source version → canonical imageopt.CacheIdentity key
//	→ disk-cache Get (hit: serve, no processor call)
//	→ otherwise acquire permit → decode/resize/encode → validate result
//	→ disk-cache Put (best effort) → serve
//
// The source fingerprint is computed from the SAME single bounded snapshot
// the processor would decode (§11): a source replaced mid-request cannot
// publish V2 bytes under V1's identity. The snapshot also feeds the
// pre-decode DecodeConfig gate, so every miss processes exactly the bytes
// that were fingerprinted.
//
// A cache hit still enforces the full request validation, format
// negotiation, root-confined resolution, byte limit, and pre-decode pixel
// gate — only decode/resize/encode are skipped. Cache failures of any
// kind degrade to processing; none of them reach the client.

// sourceVersion returns the hex SHA-256 of the bounded source snapshot.
// This is the content-address that defeats same-size/same-mtime
// replacement: identical metadata with different bytes produces a
// different version and therefore a different cache key.
func sourceVersion(srcBytes []byte) string {
	sum := sha256.Sum256(srcBytes)
	return hex.EncodeToString(sum[:])
}

// Versioned is the optional seam processors implement to fold their
// identity into cache keys (§15), mirroring the existing DecodeConfig
// seam. The native backend's Version() is stable for the process
// lifetime and includes library and pipeline versions, so an encoder
// upgrade produces fresh entries while old ones stay evictable. A
// processor without the seam gets a constant "unversioned" tag: its
// outputs are deterministic per transform anyway.
type Versioned interface {
	Version() string
}

// processorVersion resolves the processor's cache identity contribution.
func processorVersion(p imageproc.Processor) string {
	if v, ok := p.(Versioned); ok {
		if s := v.Version(); s != "" {
			return s
		}
	}
	return "unversioned"
}

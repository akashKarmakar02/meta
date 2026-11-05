package imagecache

import (
	"bytes"

	"github.com/akashKarmakar02/meta/imageopt"
)

// Integrity strategy (§25), stated honestly:
//
// The cache directory is trusted server data. Hit verification does NOT
// cryptographically prove content integrity — it detects accidental
// corruption cheaply: wrong magic bytes (format mixup, garbage overwrite),
// truncation to zero, size beyond the configured entry bound, and
// non-regular files. A deliberate attacker who can rewrite entries
// wholesale can also forge magic bytes; defending against that requires
// a keyed digest recorded somewhere the attacker cannot write, which is
// outside this trust model and documented as a limitation.
//
// Full native decode on every hit would defeat the point of the cache, so
// verification stops at magic + size + regular-file status. Publication
// additionally enforces MaxEntryBytes and the same magic check before an
// entry becomes visible.

// validFormatMagic reports whether data starts with the magic bytes of
// the expected concrete output format. It reads at most a few bytes.
func validFormatMagic(f imageopt.Format, data []byte) bool {
	switch f {
	case imageopt.FormatWebP:
		// RIFF....WEBP
		return len(data) >= 12 && bytes.HasPrefix(data, []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP"))
	case imageopt.FormatAVIF:
		// ISO-BMFF: ftyp box whose major brand starts with "avif" or
		// "avis" (common brands: avif, avis, mif1, miaf). Accept any ftyp
		// with an avif/avis brand prefix; the encoder emits "avif".
		return len(data) >= 12 && bytes.HasPrefix(data, []byte{0, 0, 0}) && bytes.HasPrefix(data[4:], []byte("ftyp")) &&
			(len(data) >= 12 && (bytes.HasPrefix(data[8:12], []byte("avif")) || bytes.HasPrefix(data[8:12], []byte("avis"))))
	case imageopt.FormatJPEG:
		return len(data) >= 2 && data[0] == 0xFF && data[1] == 0xD8 // SOI
	case imageopt.FormatPNG:
		return len(data) >= 8 && bytes.HasPrefix(data, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'})
	default:
		return false
	}
}

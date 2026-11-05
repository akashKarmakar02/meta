// Package imageopt defines the F01 image request contract. It performs no I/O,
// image processing, source resolution, or remote fetching.
package imageopt

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	Endpoint       = "/_meta/image"
	DefaultQuality = 80
	MaxDimension   = 4096
	MaxPixels      = 16_777_216
	MaxSourceBytes = 2048
	MaxQueryBytes  = 8192
)

var (
	ErrInvalidRequest    = errors.New("imageopt: invalid request")
	ErrInvalidSource     = errors.New("imageopt: invalid source")
	ErrInvalidDimensions = errors.New("imageopt: invalid dimensions")
	ErrInvalidQuality    = errors.New("imageopt: invalid quality")
	ErrInvalidFormat     = errors.New("imageopt: invalid format")
)

// Format identifies a requested or selected output; Auto is not an encoded format.
type Format string

const (
	FormatAuto Format = "auto"
	FormatAVIF Format = "avif"
	FormatWebP Format = "webp"
	FormatJPEG Format = "jpeg"
	FormatPNG  Format = "png"
)

// MIME returns the media type for a concrete format. Auto has no media type.
func (f Format) MIME() (string, error) {
	switch f {
	case FormatAVIF:
		return "image/avif", nil
	case FormatWebP:
		return "image/webp", nil
	case FormatJPEG:
		return "image/jpeg", nil
	case FormatPNG:
		return "image/png", nil
	default:
		return "", ErrInvalidFormat
	}
}

// Request is a transformation specification. Validate before use: zero width or
// height means unspecified, but at least one must be positive. Quality is 1–100.
// With both dimensions specified the image fits inside the box, preserving
// aspect ratio; cropping and upscaling policy are not implemented by F01.
type Request struct {
	Source                 string
	Width, Height, Quality int
	Format                 Format
}

// Limits can narrow the hard safety ceilings independently for each server.
// All values must be positive and at most the package ceilings.
type Limits struct {
	MaxWidth, MaxHeight int
	MaxPixels           int64
}

// DefaultLimits returns the hard safety ceilings as a fresh value.
func DefaultLimits() Limits {
	return Limits{MaxWidth: MaxDimension, MaxHeight: MaxDimension, MaxPixels: MaxPixels}
}

// Validate rejects zero, unbounded, and excessively permissive limits.
func (l Limits) Validate() error {
	if l.MaxWidth < 1 || l.MaxHeight < 1 || l.MaxWidth > MaxDimension || l.MaxHeight > MaxDimension || l.MaxPixels < 1 || l.MaxPixels > MaxPixels {
		return ErrInvalidDimensions
	}
	return nil
}

// ValidateWithin applies both the public hard ceilings and server limits.
func (r Request) ValidateWithin(l Limits) error {
	if err := l.Validate(); err != nil {
		return err
	}
	if err := r.Validate(); err != nil {
		return err
	}
	if r.Width > l.MaxWidth || r.Height > l.MaxHeight || int64(r.Width)*int64(r.Height) > l.MaxPixels {
		return ErrInvalidDimensions
	}
	return nil
}

// Validate enforces the contract for directly constructed requests.
func (r Request) Validate() error {
	if err := validateSource(r.Source); err != nil {
		return err
	}
	if r.Width < 0 || r.Height < 0 || r.Width > MaxDimension || r.Height > MaxDimension || (r.Width == 0 && r.Height == 0) || int64(r.Width)*int64(r.Height) > MaxPixels {
		return ErrInvalidDimensions
	}
	if r.Quality < 1 || r.Quality > 100 {
		return ErrInvalidQuality
	}
	if r.Format != FormatAuto {
		if _, err := r.Format.MIME(); err != nil {
			return err
		}
	}
	return nil
}

func validateSource(s string) error {
	if len(s) > MaxSourceBytes || !utf8.ValidString(s) || !(strings.HasPrefix(s, "/images/") || strings.HasPrefix(s, "/assets/")) || strings.ContainsAny(s, "%\\?#:") {
		return ErrInvalidSource
	}
	for _, ch := range s {
		if unicode.IsControl(ch) {
			return ErrInvalidSource
		}
	}
	for _, segment := range strings.Split(strings.TrimPrefix(s, "/"), "/") {
		if segment == "" || segment == "." || segment == ".." {
			return ErrInvalidSource
		}
	}
	return nil
}

// ParseRawQuery applies exactly one URL query decoding step and rejects
// malformed escapes. ParseValues expects values already decoded once.
func ParseRawQuery(raw string) (Request, error) {
	if len(raw) > MaxQueryBytes {
		return Request{}, ErrInvalidRequest
	}
	v, err := url.ParseQuery(raw)
	if err != nil {
		return Request{}, ErrInvalidRequest
	}
	return ParseValues(v)
}

// ParseValues rejects unknown, duplicate and explicitly empty parameters.
// Missing q and f default to 80 and auto. Values must be query-decoded once.
func ParseValues(v url.Values) (Request, error) {
	var size int
	for key, values := range v {
		size += len(key)
		switch key {
		case "src", "w", "h", "q", "f":
		default:
			return Request{}, ErrInvalidRequest
		}
		if len(values) != 1 || values[0] == "" {
			return Request{}, ErrInvalidRequest
		}
		size += len(values[0])
		if size > MaxQueryBytes {
			return Request{}, ErrInvalidRequest
		}
	}
	if len(v["src"]) != 1 {
		return Request{}, ErrInvalidSource
	}
	r := Request{Source: v.Get("src"), Quality: DefaultQuality, Format: FormatAuto}
	for _, p := range []struct {
		key      string
		dst      *int
		category error
	}{{"w", &r.Width, ErrInvalidDimensions}, {"h", &r.Height, ErrInvalidDimensions}, {"q", &r.Quality, ErrInvalidQuality}} {
		if x, ok := v[p.key]; ok {
			s := x[0]
			for _, ch := range s {
				if ch < '0' || ch > '9' {
					return Request{}, p.category
				}
			}
			n, err := strconv.Atoi(s)
			if err != nil || n == 0 {
				return Request{}, p.category
			}
			*p.dst = n
		}
	}
	if x, ok := v["f"]; ok {
		r.Format = Format(x[0])
	}
	if err := r.Validate(); err != nil {
		return Request{}, err
	}
	return r, nil
}

// URL validates r and builds a deterministic root-relative endpoint URL.
func URL(r Request) (string, error) {
	if err := r.Validate(); err != nil {
		return "", err
	}
	v := url.Values{"src": {r.Source}, "q": {strconv.Itoa(r.Quality)}, "f": {string(r.Format)}}
	if r.Width > 0 {
		v.Set("w", strconv.Itoa(r.Width))
	}
	if r.Height > 0 {
		v.Set("h", strconv.Itoa(r.Height))
	}
	return Endpoint + "?" + v.Encode(), nil
}

// CacheIdentity supplies trusted resolver and processor versions. It is not a
// cache implementation; SourceVersion must change when source content changes.
// Namespace identifies the configured root, not a physical path or a secret.
type CacheIdentity struct {
	Request                                    Request
	Namespace, SourceVersion, ProcessorVersion string
	SelectedFormat                             Format
}

// Key returns a versioned SHA-256 digest suitable for a future storage key.
// It is deliberately independent of the public URL and filesystem layout.
func (id CacheIdentity) Key() (string, error) {
	if err := id.Request.Validate(); err != nil {
		return "", err
	}
	if _, err := id.SelectedFormat.MIME(); err != nil {
		return "", err
	}
	if id.Request.Format != FormatAuto && id.Request.Format != id.SelectedFormat {
		return "", ErrInvalidFormat
	}
	for _, s := range []string{id.Namespace, id.SourceVersion, id.ProcessorVersion} {
		if len(s) == 0 || len(s) > 1024 || !utf8.ValidString(s) || strings.IndexFunc(s, unicode.IsControl) >= 0 {
			return "", ErrInvalidRequest
		}
	}
	fields := []string{"go-meta-image-v1", id.Namespace, id.Request.Source, id.SourceVersion, strconv.Itoa(id.Request.Width), strconv.Itoa(id.Request.Height), "contain", strconv.Itoa(id.Request.Quality), string(id.SelectedFormat), id.ProcessorVersion}
	h := sha256.New()
	var n [8]byte
	for _, s := range fields {
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		_, _ = h.Write(n[:])
		_, _ = h.Write([]byte(s))
	}
	return fmt.Sprintf("v1-%s", hex.EncodeToString(h.Sum(nil))), nil
}

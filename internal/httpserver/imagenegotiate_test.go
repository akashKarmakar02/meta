package httpserver

import (
	"testing"

	"go-meta/imageproc"
)

// fullCaps is the native backend's probed capability set on this host.
func fullCaps() imageproc.Capabilities {
	return imageproc.Capabilities{
		JPEGDecode: true, PNGDecode: true, WebPDecode: true,
		JPEGEncode: true, PNGEncode: true, WebPEncode: true, AVIFEncode: true,
	}
}

// TestSelectFormat covers the Accept negotiation matrix (F03 §39).
func TestSelectFormat(t *testing.T) {
	tests := []struct {
		name   string
		accept string
		caps   imageproc.Capabilities
		want   string // "" means no acceptable format (HTTP 406)
	}{
		{name: "avif-first", accept: "image/avif,image/webp", caps: fullCaps(), want: "avif"},
		{name: "webp-only", accept: "image/webp", caps: fullCaps(), want: "webp"},
		{name: "avif-excluded", accept: "image/avif;q=0,image/webp;q=1", caps: fullCaps(), want: "webp"},
		{name: "weighted-webp", accept: "image/avif;q=0.2,image/webp;q=0.9", caps: fullCaps(), want: "webp"},
		{name: "weighted-avif", accept: "image/avif;q=0.9,image/webp;q=0.2", caps: fullCaps(), want: "avif"},
		{name: "wildcard-image", accept: "image/*", caps: fullCaps(), want: "webp"}, // deterministic default
		{name: "wildcard-all", accept: "*/*", caps: fullCaps(), want: "webp"},
		{name: "jpeg-only", accept: "image/jpeg", caps: fullCaps(), want: "jpeg"},
		{name: "gif-only", accept: "image/gif", caps: fullCaps(), want: ""}, // talks images, none acceptable
		{name: "invalid", accept: "not a media type at all", caps: fullCaps(), want: "webp"},
		{name: "missing", accept: "", caps: fullCaps(), want: "webp"},
		{name: "browser-realistic", accept: "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8", caps: fullCaps(), want: "avif"},
		{name: "q-not-substring", accept: "image/avif;q=0.5,image/webp;q=0.51", caps: fullCaps(), want: "webp"},
		{name: "malformed-q-ignored", accept: "garbage;q=abc,image/avif", caps: fullCaps(), want: "avif"},
		{name: "quoted-q", accept: `image/webp;q="0.9"`, caps: fullCaps(), want: "webp"},
		// Capability-restricted processors.
		{name: "no-avif-cap", accept: "image/avif,image/webp", caps: noAVIF(), want: "webp"},
		{name: "no-webp-cap", accept: "image/avif,image/webp", caps: noWebP(), want: "avif"},
		{name: "no-both-cap", accept: "image/avif,image/webp,image/jpeg", caps: jpegOnly(), want: "jpeg"},
		{name: "no-both-cap-wildcard", accept: "image/*", caps: jpegOnly(), want: "jpeg"},
		{name: "no-encodable-at-all", accept: "image/*", caps: imageproc.Capabilities{}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := selectFormat(tt.accept, tt.caps)
			if string(got) != tt.want {
				t.Fatalf("selectFormat(%q) = %q, want %q", tt.accept, got, tt.want)
			}
		})
	}
}

func noAVIF() imageproc.Capabilities {
	c := fullCaps()
	c.AVIFEncode = false
	return c
}

func noWebP() imageproc.Capabilities {
	c := fullCaps()
	c.WebPEncode = false
	return c
}

func jpegOnly() imageproc.Capabilities {
	return imageproc.Capabilities{JPEGDecode: true, PNGDecode: true, JPEGEncode: true}
}

// TestSelectFormatMIMEAgreement: whatever gets selected must have a valid
// canonical MIME type (no fabricated content types reach responses).
func TestSelectFormatMIMEAgreement(t *testing.T) {
	for _, accept := range []string{"image/*", "*/*", "image/avif,image/webp", ""} {
		f := selectFormat(accept, fullCaps())
		if f == "" {
			continue
		}
		m, err := f.MIME()
		if err != nil || m == "" {
			t.Fatalf("%q → %v: bad MIME %q %v", accept, f, m, err)
		}
		switch m {
		case "image/avif", "image/webp", "image/jpeg", "image/png":
		default:
			t.Fatalf("non-canonical MIME %q", m)
		}
	}
}

// TestParseMediaType pins the focused parser's element semantics.
func TestParseMediaType(t *testing.T) {
	if mt, q, ok := parseMediaType("image/webp"); !ok || mt != "image/webp" || q != 1 {
		t.Fatalf("plain: %q %v %v", mt, q, ok)
	}
	if mt, q, ok := parseMediaType("IMAGE/AVIF ; q=0.5"); !ok || mt != "image/avif" || q != 0.5 {
		t.Fatalf("case+param: %q %v %v", mt, q, ok)
	}
	if _, _, ok := parseMediaType("nosubtype"); ok {
		t.Fatal("missing slash accepted")
	}
	if _, _, ok := parseMediaType("image/webp;q=2"); ok {
		t.Fatal("out-of-range q accepted")
	}
	if _, _, ok := parseMediaType("image/webp;q=abc"); ok {
		t.Fatal("malformed q accepted")
	}
	if _, q, ok := parseMediaType("text/html;q=0"); !ok || q != 0 {
		t.Fatal("explicit zero should parse as exclusion")
	}
}

// TestSplitMediaTypes: commas inside quotes do not split.
func TestSplitMediaTypes(t *testing.T) {
	parts := splitMediaTypes(`image/webp;q="a,b",image/avif`)
	if len(parts) != 2 {
		t.Fatalf("parts = %d (%v)", len(parts), parts)
	}
}

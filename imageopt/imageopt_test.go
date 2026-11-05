package imageopt

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

func TestParseRawQuery(t *testing.T) {
	cases := []struct {
		name, raw string
		want      error
	}{
		{"width", "src=%2Fimages%2Fhero.jpg&w=960", nil}, {"height", "src=%2Fassets%2F%E9%9B%AA.png&h=4", nil},
		{"both", "src=%2Fimages%2Fa&w=10&h=20&q=1&f=webp", nil}, {"maximum quality", "src=%2Fimages%2Fa&w=1&q=100&f=avif", nil},
		{"missing source", "w=1", ErrInvalidSource}, {"empty source", "src=&w=1", ErrInvalidRequest}, {"source prefix", "src=%2Fprivate%2Fa&w=1", ErrInvalidSource},
		{"no dimensions", "src=%2Fimages%2Fa", ErrInvalidDimensions}, {"zero", "src=%2Fimages%2Fa&w=0", ErrInvalidDimensions}, {"negative", "src=%2Fimages%2Fa&w=-1", ErrInvalidDimensions},
		{"large", "src=%2Fimages%2Fa&w=4097", ErrInvalidDimensions}, {"overflow", "src=%2Fimages%2Fa&w=99999999999999999999999", ErrInvalidDimensions}, {"fraction", "src=%2Fimages%2Fa&w=1.5", ErrInvalidDimensions},
		{"quality zero", "src=%2Fimages%2Fa&w=1&q=0", ErrInvalidQuality}, {"quality high", "src=%2Fimages%2Fa&w=1&q=101", ErrInvalidQuality},
		{"format", "src=%2Fimages%2Fa&w=1&f=gif", ErrInvalidFormat}, {"duplicate", "src=%2Fimages%2Fa&w=1&w=2", ErrInvalidRequest}, {"duplicate same", "src=%2Fimages%2Fa&w=1&w=1", ErrInvalidRequest},
		{"unknown", "src=%2Fimages%2Fa&w=1&x=1", ErrInvalidRequest}, {"empty width", "src=%2Fimages%2Fa&w=", ErrInvalidRequest}, {"bad escape", "src=%zz&w=1", ErrInvalidRequest},
		{"too long", strings.Repeat("a", MaxQueryBytes+1), ErrInvalidRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseRawQuery(tc.raw)
			if !errors.Is(err, tc.want) {
				t.Fatalf("%+v %v want %v", got, err, tc.want)
			}
			if err == nil && got.Quality < 1 {
				t.Fatal("default quality missing")
			}
		})
	}
}
func TestUnsafeSources(t *testing.T) {
	for _, src := range []string{"/../../etc/passwd", "/%2e%2e/private", "file:///etc/passwd", "http://127.0.0.1/admin", "http://169.254.169.254/", "https://unapproved.example/image.jpg", "//evil.example/image.png", `C:\Windows\secret.png`, "/images/../secret", "/images/./x", "/images//x", `/images/..\secret`, "/images/%2e%2e/a", "/images/%2fa", "/images/%252e%252e/x", "/images/\x00x", "/images/a\n", "/images/" + strings.Repeat("x", MaxSourceBytes), "/images/\xff"} {
		t.Run(src, func(t *testing.T) {
			_, err := ParseValues(url.Values{"src": {src}, "w": {"1"}})
			if !errors.Is(err, ErrInvalidSource) {
				t.Fatalf("source accepted: %q: %v", src, err)
			}
		})
	}
	for _, raw := range []string{"src=%2Fimages%2F%252e%252e%2Fsecret&w=1", "src=%2Fimages%2Fa%00b&w=1", "src=%2F%2Fetc%2Fpasswd&w=1"} {
		if _, err := ParseRawQuery(raw); !errors.Is(err, ErrInvalidSource) {
			t.Fatalf("%s: %v", raw, err)
		}
	}
}
func TestCanonicalURLAndKey(t *testing.T) {
	a, err := ParseRawQuery("src=%2Fimages%2F%E9%9B%AA.jpg&w=960")
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseRawQuery("q=80&f=auto&w=960&src=%2Fimages%2F%E9%9B%AA.jpg")
	if err != nil || a != b {
		t.Fatalf("%+v %+v %v", a, b, err)
	}
	u, err := URL(a)
	if err != nil || !strings.HasPrefix(u, Endpoint+"?") {
		t.Fatalf("%q %v", u, err)
	}
	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatal(err)
	}
	round, err := ParseRawQuery(parsed.RawQuery)
	if err != nil || round != a {
		t.Fatalf("%+v %v", round, err)
	}
	id := CacheIdentity{Request: a, Namespace: "public", SourceVersion: "v1", ProcessorVersion: "encoder1", SelectedFormat: FormatWebP}
	key, err := id.Key()
	if err != nil || len(key) != 67 {
		t.Fatalf("%s %v", key, err)
	}
	again, _ := id.Key()
	if key != again {
		t.Fatal("unstable key")
	}
	variants := []CacheIdentity{id, id, id, id, id, id, id}
	variants[0].Request.Width++
	variants[1].Request.Height = 3
	variants[2].Request.Source = "/images/other"
	variants[3].Namespace = "public2"
	variants[4].SourceVersion = "v2"
	variants[5].ProcessorVersion = "encoder2"
	variants[6].SelectedFormat = FormatAVIF
	for _, v := range variants {
		k, err := v.Key()
		if err != nil || k == key {
			t.Fatalf("identity collision %q %v", k, err)
		}
	}
	id.Request.Format = FormatJPEG
	if _, err := id.Key(); !errors.Is(err, ErrInvalidFormat) {
		t.Fatalf("format mismatch: %v", err)
	}
	id.Request.Format = FormatAuto
	id.SourceVersion = ""
	if _, err := id.Key(); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal(err)
	}
}
func TestFormatMIMEAndDirectValidation(t *testing.T) {
	for f, m := range map[Format]string{FormatAVIF: "image/avif", FormatWebP: "image/webp", FormatJPEG: "image/jpeg", FormatPNG: "image/png"} {
		got, err := f.MIME()
		if err != nil || got != m {
			t.Fatalf("%s %s %v", f, got, err)
		}
	}
	if _, err := FormatAuto.MIME(); !errors.Is(err, ErrInvalidFormat) {
		t.Fatal(err)
	}
	if _, err := URL(Request{Source: "/images/a", Width: 1, Format: FormatAuto}); !errors.Is(err, ErrInvalidQuality) {
		t.Fatal(err)
	}
}

func TestIndependentLimits(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxWidth = 640
	limits.MaxHeight = 480
	limits.MaxPixels = 100000
	valid := Request{Source: "/images/a", Width: 320, Height: 200, Quality: 80, Format: FormatAuto}
	if err := valid.ValidateWithin(limits); err != nil {
		t.Fatal(err)
	}
	tooWide := valid
	tooWide.Width = 641
	if !errors.Is(tooWide.ValidateWithin(limits), ErrInvalidDimensions) {
		t.Fatal("width limit")
	}
	tooMany := valid
	tooMany.Width = 500
	tooMany.Height = 300
	if !errors.Is(tooMany.ValidateWithin(limits), ErrInvalidDimensions) {
		t.Fatal("pixel limit")
	}
	for _, bad := range []Limits{{}, {MaxWidth: MaxDimension + 1, MaxHeight: 1, MaxPixels: 1}, {MaxWidth: 1, MaxHeight: 1, MaxPixels: MaxPixels + 1}} {
		if !errors.Is(bad.Validate(), ErrInvalidDimensions) {
			t.Fatal("invalid configuration accepted")
		}
	}
	if err := valid.ValidateWithin(DefaultLimits()); err != nil {
		t.Fatal(err)
	}
}

package imageopt

import (
	"testing"
)

// F04 fuzz targets: every parser and validator that touches
// attacker-controlled input must be total — no panics, no unbounded
// memory growth, no nontermination — for arbitrary bytes. Seeds cover
// valid and malformed shapes. These targets never touch the filesystem
// or network: they operate on strings only.

func FuzzParseRawQuery(f *testing.F) {
	for _, seed := range []string{
		"src=%2Fimages%2Fhero.jpg&w=400&q=80&f=webp",
		"src=%2Fassets%2Fa.png",
		"src=",
		"src=..%2F..%2Fetc%2Fpasswd&w=10",
		"src=%2Fimages%2Fx.jpg&w=99999999999999999999",
		"src=%2Fimages%2Fx.jpg&q=-1&f=svg",
		"w=100&h=100",
		"a=1&a=2",
		"src=%2Fimages%2Fx.jpg&unknown=1",
		"%zz",
		"src=%2Fimages%2Fx.jpg&f=",
		"&&&&",
		"\x00\x01\x02",
		"src=%2Fimages%2F" + string(make([]byte, 4096)),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		r, err := ParseRawQuery(raw)
		if err != nil {
			return // rejected safely
		}
		// Whatever was accepted must also validate standalone.
		if verr := r.Validate(); verr != nil {
			t.Fatalf("ParseRawQuery accepted %q but Validate rejects: %v", raw, verr)
		}
	})
}

func FuzzValidateSource(f *testing.F) {
	for _, seed := range []string{
		"/images/hero.jpg",
		"/assets/a.png",
		"",
		"../etc/passwd",
		"/images/../secret",
		"/images/a\\b.jpg",
		"/images/%2e%2e/x",
		"http://evil.example/x.jpg",
		"//evil.example/x.jpg",
		"/private/secret",
		"/images/" + string([]byte{0x00}),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if err := validateSource(s); err == nil {
			// Accepted sources must stay within the two public namespaces
			// with no escapes.
			if !(len(s) <= MaxSourceBytes &&
				(string(s[0:8]) == "/images/" || string(s[0:8]) == "/assets/")) {
				t.Fatalf("validateSource accepted unsafe %q", s)
			}
		}
	})
}

func FuzzCacheIdentityKey(f *testing.F) {
	for _, seed := range []string{
		"src=%2Fimages%2Fhero.jpg&w=400&f=webp",
		"src=%2Fimages%2Fx.png&h=200&f=auto",
		"src=%2Fassets%2Fy.webp&w=0&q=1&f=jpeg",
		"src=%zzzz",
	} {
		f.Add(seed, "ns", "v1", "proc", "webp")
	}
	f.Fuzz(func(t *testing.T, raw, ns, srcVer, procVer, format string) {
		r, err := ParseRawQuery(raw)
		if err != nil {
			return
		}
		id := CacheIdentity{
			Request:          r,
			Namespace:        ns,
			SourceVersion:    srcVer,
			ProcessorVersion: procVer,
			SelectedFormat:   Format(format),
		}
		key, kerr := id.Key()
		if kerr == nil {
			// A produced key must be deterministic and well-formed.
			again, _ := id.Key()
			if key != again {
				t.Fatalf("cache key not deterministic for %q", raw)
			}
			if len(key) < 8 || len(key) > 256 {
				t.Fatalf("implausible key length %d", len(key))
			}
		}
	})
}

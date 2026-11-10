package sitemap

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/akashKarmakar02/meta/cache"
)

func TestNewRequiresBaseURLAndSource(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("expected error for missing baseURL")
	}
	if _, err := New(Config{BaseURL: "https://example.com"}); err == nil {
		t.Fatal("expected error for missing route source")
	}
	for _, bad := range []string{"example.com", "ftp://example.com", "https://", "https://example.com/q?x=1"} {
		if _, err := New(Config{BaseURL: bad, RouteSource: Source{}}); err == nil {
			t.Fatalf("expected error for baseURL %q", bad)
		}
	}
	if _, err := New(Config{BaseURL: "https://example.com/", RouteSource: Source{}}); err != nil {
		t.Fatalf("valid baseURL rejected: %v", err)
	}
}

func TestStaticRoutesOnly(t *testing.T) {
	s, err := New(Config{
		BaseURL:     "https://example.com",
		RouteSource: Source{{Path: "/"}, {Path: "/blog"}, {Path: "/blog/[slug]"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	xml, err := s.XML(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(xml), "<loc>https://example.com/</loc>") {
		t.Error("root URL missing")
	}
	if !strings.Contains(string(xml), "<loc>https://example.com/blog</loc>") {
		t.Error("blog URL missing")
	}
	if strings.Contains(string(xml), "slug") {
		t.Error("dynamic route leaked into static sitemap")
	}
}

func TestDynamicRoutesResolvedAndSorted(t *testing.T) {
	s, err := New(Config{
		BaseURL: "https://example.com",
		RouteSource: Source{
			{Path: "/z"},
			{Path: "/blog/[slug]"},
		},
		Dynamic: map[string]DynamicURLs{
			"/blog/[slug]": func(ctx context.Context) ([]URL, error) {
				return []URL{
					{Loc: "/blog/b"},
					{Loc: "/blog/a", LastMod: time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)},
				}, nil
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	xml, err := s.XML(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	aIdx := strings.Index(string(xml), "/blog/a")
	bIdx := strings.Index(string(xml), "/blog/b")
	zIdx := strings.Index(string(xml), "/z<")
	if aIdx < 0 || bIdx < 0 || zIdx < 0 || !(aIdx < bIdx && bIdx < zIdx) {
		t.Errorf("URLs not sorted: a=%d b=%d z=%d", aIdx, bIdx, zIdx)
	}
	if !strings.Contains(string(xml), "<lastmod>2025-01-02</lastmod>") {
		t.Error("lastmod missing or wrongly formatted")
	}
}

func TestDynamicErrorFailsBuild(t *testing.T) {
	s, err := New(Config{
		BaseURL:     "https://example.com",
		RouteSource: Source{{Path: "/blog/[slug]"}},
		Dynamic: map[string]DynamicURLs{
			"/blog/[slug]": func(ctx context.Context) ([]URL, error) { return nil, errors.New("db down") },
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.XML(context.Background()); err == nil || !strings.Contains(err.Error(), "db down") {
		t.Fatalf("expected wrapped db error, got %v", err)
	}
}

func TestInvalidateRebuilds(t *testing.T) {
	calls := 0
	s, err := New(Config{
		BaseURL:     "https://example.com",
		RouteSource: Source{{Path: "/"}},
		Dynamic:     nil,
		Now:         func() time.Time { return time.Unix(1000, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = calls
	ctx := context.Background()
	first, err := s.XML(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.XML(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Error("memory cache should return identical XML")
	}
	s.Invalidate(ctx)
	third, err := s.XML(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(third) {
		t.Error("regenerated XML differs")
	}
}

func TestPersistenceAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	store, err := cache.NewDiskStore(cache.DiskConfig{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	s1, err := New(Config{
		BaseURL:     "https://example.com",
		RouteSource: Source{{Path: "/persisted"}},
		Store:       store,
	})
	if err != nil {
		t.Fatal(err)
	}
	xml1, err := s1.XML(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Simulated restart: same store, fresh Sitemap.
	s2, err := New(Config{
		BaseURL:     "https://example.com",
		RouteSource: Source{{Path: "/changed"}},
		Store:       store,
	})
	if err != nil {
		t.Fatal(err)
	}
	xml2, err := s2.XML(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(xml1) != string(xml2) {
		t.Error("disk-persisted XML should be served after restart")
	}
	if strings.Contains(string(xml2), "/changed") {
		t.Error("stale routes must not leak; persisted copy should win after restart")
	}
}

func TestXMLEscaping(t *testing.T) {
	if _, err := New(Config{
		BaseURL:     "https://example.com",
		RouteSource: Source{{Path: "/"}},
	}); err != nil {
		t.Fatal(err)
	}
	// Reach into render directly with hostile input to prove escaping.
	xml, err := render([]URL{{Loc: "/a&b<c", ChangeFreq: "</changefreq><x>", Priority: "\""}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(xml), "</changefreq><x>") {
		t.Error("injection via changefreq")
	}
	if !strings.Contains(string(xml), "&amp;") {
		t.Error("loc not escaped")
	}
}

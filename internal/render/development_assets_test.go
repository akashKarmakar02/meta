package render

import (
	"github.com/akashKarmakar02/meta/internal/httpserver"
	"strings"
	"testing"
)

func TestDevelopmentEntryTags(t *testing.T) {
	assets, err := httpserver.DevelopmentAssets("http://127.0.0.1:5173")
	if err != nil {
		t.Fatal(err)
	}
	tags := entryTags(&assets)
	if strings.Count(tags, "@vite/client") != 1 || strings.Count(tags, "frontend/entry.ts") != 1 {
		t.Fatal(tags)
	}
	production := entryTags(&httpserver.Assets{EntryScriptURL: "/assets/entry.js"})
	if strings.Contains(production, "@vite/client") || !strings.Contains(production, "/assets/entry.js") {
		t.Fatal(production)
	}
}

func TestDevelopmentStylesheetAppearsInHead(t *testing.T) {
	assets := &httpserver.Assets{DevClientURL: "/_go_meta_vite/@vite/client", EntryScriptURL: "/_go_meta_vite/frontend/entry.ts", EntryCSSURLs: []string{"/_go_meta_vite/frontend/app.css?direct"}}
	html := document("<main>content</main>", entryTags(assets), "test", "")
	headEnd := strings.Index(html, "</head>")
	css := strings.Index(html, `<link rel="stylesheet"`)
	if css < 0 || headEnd < 0 || css > headEnd {
		t.Fatalf("stylesheet not in head: %s", html)
	}
	if strings.Count(html, "frontend/app.css?direct") != 1 {
		t.Fatalf("duplicate or missing stylesheet: %s", html)
	}
}

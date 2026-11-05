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

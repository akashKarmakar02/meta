package httpserver

import (
	"strings"
	"testing"
)

func TestDevelopmentAssets(t *testing.T) {
	a, err := DevelopmentAssets("http://127.0.0.1:5173")
	if err != nil {
		t.Fatal(err)
	}
	if a.DevClientURL != "http://127.0.0.1:5173/@vite/client" || a.EntryScriptURL != "http://127.0.0.1:5173/frontend/entry.ts" {
		t.Fatalf("unexpected assets: %+v", a)
	}
	for _, origin := range []string{"https://example.com:5173", "http://evil.example:5173", "http://127.0.0.1:5173/path", "http://user@127.0.0.1:5173"} {
		if _, err := DevelopmentAssets(origin); err == nil || !strings.Contains(err.Error(), "Vite") {
			t.Errorf("accepted %q: %v", origin, err)
		}
	}
}

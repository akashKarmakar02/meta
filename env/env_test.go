package env_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/akashKarmakar02/meta/env"
)

func TestLoadProductionPrecedence(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		".env":            "META_ENV_PROD_TEST=base\nMETA_ENV_SHELL_TEST=file\n",
		".env.local":      "META_ENV_PROD_TEST=local\n",
		".env.production": "META_ENV_PROD_TEST=production\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("META_ENV_PROD_TEST", "")
	if err := os.Unsetenv("META_ENV_PROD_TEST"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("META_ENV_SHELL_TEST", "shell")
	if err := env.LoadProduction(root); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("META_ENV_PROD_TEST"); got != "production" {
		t.Fatalf("production value = %q", got)
	}
	if got := os.Getenv("META_ENV_SHELL_TEST"); got != "shell" {
		t.Fatalf("shell value = %q", got)
	}
}

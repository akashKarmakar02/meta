package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akashKarmakar02/meta/internal/envfile"
)

func TestDevelopmentEnvPrecedenceAndIsolation(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		".env":            "META_TEST_A=base\nMETA_TEST_B='a # b'\nMETA_TEST_SHELL=file\nPORT=9999\n",
		".env.local":      "META_TEST_A=highest\nMETA_TEST_C=\"hello\\nworld\"\n",
		".env.production": "META_TEST_A=ignored\n",
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("META_TEST_SHELL", "shell")
	t.Setenv("PORT", "")
	if err := os.Unsetenv("PORT"); err != nil {
		t.Fatal(err)
	}
	got, err := developmentEnv(root)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(got, "\x00")
	for _, want := range []string{"META_TEST_A=highest", "META_TEST_B=a # b", "META_TEST_C=hello\nworld"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(joined, "META_TEST_SHELL=") || !strings.Contains(joined, "PORT=9999") {
		t.Fatal("shell precedence or file port handling failed")
	}
	if devEnvValue("PORT", got) != "9999" {
		t.Fatal("supervisor did not receive the file port")
	}
	if _, ok := os.LookupEnv("META_TEST_A"); ok {
		t.Fatal("file value leaked to parent process")
	}
}

func TestDevelopmentEnvInvalidDoesNotExposeValue(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("PRIVATE_KEY=\"secret-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := developmentEnv(root)
	if err == nil || !strings.Contains(err.Error(), ".env:1") || strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("error: %v", err)
	}
}

func TestParseEnvValue(t *testing.T) {
	for _, tc := range []struct {
		input, want string
		bad         bool
	}{
		{"value # comment", "value", false},
		{"'value # literal'", "value # literal", false},
		{`"value\nline"`, "value\nline", false},
		{"$OTHER", "$OTHER", false},
		{"'missing", "", true},
		{"'value' trailing", "", true},
	} {
		got, err := envfile.ParseValue(tc.input)
		if (err != nil) != tc.bad || (!tc.bad && got != tc.want) {
			t.Errorf("parse %q: %q, %v", tc.input, got, err)
		}
	}
}

func TestPublicBuildEnvUsesProductionFiles(t *testing.T) {
	root := t.TempDir()
	for name, content := range map[string]string{
		".env":            "VITE_META_TEST_PUBLIC=base\nMETA_TEST_SECRET=private\n",
		".env.local":      "VITE_META_TEST_PUBLIC=local\n",
		".env.production": "VITE_META_TEST_PUBLIC=production\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("VITE_META_TEST_PUBLIC", "")
	if err := os.Unsetenv("VITE_META_TEST_PUBLIC"); err != nil {
		t.Fatal(err)
	}
	got, err := fileEnv(root, "production", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "VITE_META_TEST_PUBLIC=production" {
		t.Fatalf("public build env = %q", got)
	}
}

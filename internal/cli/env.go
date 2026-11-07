package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/akashKarmakar02/meta/internal/envfile"
)

// fileEnv returns file values not already supplied by the shell. Only public
// VITE_ values are passed to Vite; backend values are passed only to Air.
func fileEnv(root, mode string, publicOnly bool) ([]string, error) {
	values, err := envfile.Read(root, mode)
	if err != nil {
		return nil, fmt.Errorf("meta %s: %w", mode, err)
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		if _, exists := os.LookupEnv(k); exists || reservedDevEnv(k) || (publicOnly && !strings.HasPrefix(k, "VITE_")) {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, k := range keys {
		result = append(result, k+"="+values[k])
	}
	return result, nil
}

func developmentEnv(root string) ([]string, error) { return fileEnv(root, "development", false) }

// Supervisor settings must come from the shell, so the proxy and backend
// cannot receive conflicting values. GO_META_DEV is owned by the CLI.
func reservedDevEnv(key string) bool {
	switch key {
	case "GO_META_PUBLIC_PORT", "GO_META_VITE_ORIGIN", "GO_META_DEV_SOCKET", "GO_META_DEV":
		return true
	}
	return false
}

// devEnvValue applies the shell-first rule to supervisor configuration too.
func devEnvValue(key string, values []string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	prefix := key + "="
	for _, entry := range values {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	return ""
}

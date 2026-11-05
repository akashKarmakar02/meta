// Package frontend embeds the client runtime used by the project initializer.
package frontend

import (
	"embed"
	"io/fs"
	"strings"
)

//go:embed *.ts
var sources embed.FS

// RuntimeSources returns copies of the TypeScript runtime sources. Generated
// route registries and tests belong to the consuming project and are omitted.
func RuntimeSources() (map[string][]byte, error) {
	entries, err := fs.ReadDir(sources, ".")
	if err != nil {
		return nil, err
	}
	out := make(map[string][]byte)
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, ".test.ts") || strings.HasSuffix(name, ".generated.ts") {
			continue
		}
		data, err := sources.ReadFile(name)
		if err != nil {
			return nil, err
		}
		out[name] = data
	}
	return out, nil
}

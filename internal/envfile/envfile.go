// Package envfile reads Meta environment files without mutating the process.
package envfile

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

// Read applies the requested mode's file priority. Shell environment variables
// are deliberately not consulted, so callers can choose how to apply them.
func Read(root, mode string) (map[string]string, error) {
	values := make(map[string]string)
	files := []string{".env"}
	switch mode {
	case "development":
		files = append(files, ".env.local")
	case "production":
		files = append(files, ".env.production")
	default:
		return nil, fmt.Errorf("invalid environment mode %q", mode)
	}
	for _, name := range files {
		if err := readEnvFile(filepath.Join(root, name), values); err != nil {
			return nil, err
		}
	}
	return values, nil
}

func readEnvFile(path string, values map[string]string) error {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\ufeff"))
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		if strings.HasPrefix(text, "export ") {
			text = strings.TrimSpace(strings.TrimPrefix(text, "export "))
		}
		name, raw, ok := strings.Cut(text, "=")
		name = strings.TrimSpace(name)
		if !ok || !validEnvKey(name) {
			return fmt.Errorf("%s:%d: invalid environment assignment", filepath.Base(path), line)
		}
		value, err := ParseValue(strings.TrimSpace(raw))
		if err == nil && strings.ContainsRune(value, '\x00') {
			err = fmt.Errorf("NUL in environment value")
		}
		if err != nil {
			return fmt.Errorf("%s:%d: invalid environment value", filepath.Base(path), line)
		}
		values[name] = value
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	return nil
}

func validEnvKey(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		if r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

func ParseValue(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if raw[0] == '\'' || raw[0] == '"' {
		quote := raw[0]
		escaped := false
		for i := 1; i < len(raw); i++ {
			if quote == '"' && !escaped && raw[i] == '\\' {
				escaped = true
				continue
			}
			if !escaped && raw[i] == quote {
				tail := strings.TrimSpace(raw[i+1:])
				if tail != "" && !strings.HasPrefix(tail, "#") {
					return "", fmt.Errorf("trailing characters")
				}
				if quote == '\'' {
					return raw[1:i], nil
				}
				return strconv.Unquote(raw[:i+1])
			}
			escaped = false
		}
		return "", fmt.Errorf("unclosed quote")
	}
	if i := strings.IndexByte(raw, '#'); i >= 0 {
		raw = raw[:i]
	}
	return strings.TrimRightFunc(raw, unicode.IsSpace), nil
}

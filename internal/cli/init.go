package cli

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"

	"github.com/akashKarmakar02/meta/frontend"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

const frameworkModule = "github.com/akashKarmakar02/meta"

//go:embed all:scaffold
var starter embed.FS

func init() {
	register(&Command{
		Name: "init", Short: "Create a new meta project",
		Usage: `Create a new independent meta project.

Usage:
  meta init <directory> [--module <module-path>] [--framework-dir <checkout>]

The default module path is example.com/<directory-name>. --framework-dir is
for development of meta itself: it uses that local checkout via go.mod replace.
The destination must not already exist. Dependencies are installed separately
with meta setup; meta init does not access the network.
`,
		Run: runInit,
	})
}

func runInit(ctx *Context, args []string) error {
	var dest, modulePath, frameworkDir string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-h", "--help":
			fmt.Fprint(ctx.Stdout, commands["init"].Usage)
			return nil
		case "--module", "--framework-dir":
			option := args[i]
			i++
			if i >= len(args) || args[i] == "" {
				return fmt.Errorf("meta init: %s requires a value", option)
			}
			if option == "--module" {
				if modulePath != "" {
					return fmt.Errorf("meta init: --module specified more than once")
				}
				modulePath = args[i]
			} else {
				if frameworkDir != "" {
					return fmt.Errorf("meta init: --framework-dir specified more than once")
				}
				frameworkDir = args[i]
			}
		default:
			if strings.HasPrefix(args[i], "-") || dest != "" {
				return fmt.Errorf("meta init: expected one destination; see meta help init")
			}
			dest = args[i]
		}
	}
	if ctx.dir != "" {
		return fmt.Errorf("meta init: --dir is for existing projects; pass the new directory as an argument")
	}
	if dest == "" {
		return fmt.Errorf("meta init: missing destination; see meta help init")
	}
	base := filepath.Base(filepath.Clean(dest))
	if base == "." || base == ".." || base == string(filepath.Separator) {
		return fmt.Errorf("meta init: invalid destination %q", dest)
	}
	if modulePath == "" {
		modulePath = "example.com/" + base
	}
	if err := module.CheckImportPath(modulePath); err != nil {
		return fmt.Errorf("meta init: invalid module path %q: %w", modulePath, err)
	}

	version, replacement, err := initFramework(frameworkDir)
	if err != nil {
		return err
	}
	files := make(map[string][]byte)
	err = fs.WalkDir(starter, "scaffold", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !strings.HasSuffix(name, ".tmpl") {
			return fmt.Errorf("meta init: unexpected embedded file %q", name)
		}
		data, err := starter.ReadFile(name)
		if err != nil {
			return err
		}
		content := strings.NewReplacer("{{MODULE}}", modulePath, "{{META_VERSION}}", version, "{{META_REPLACE}}", replacement).Replace(string(data))
		files[strings.TrimSuffix(strings.TrimPrefix(name, "scaffold/"), ".tmpl")] = []byte(content)
		return nil
	})
	if err != nil {
		return fmt.Errorf("meta init: read scaffold: %w", err)
	}
	client, err := frontend.RuntimeSources()
	if err != nil {
		return fmt.Errorf("meta init: read client runtime: %w", err)
	}
	for name, data := range client {
		if name == "entry.ts" {
			data = append([]byte("import './app.css';\n\n"), data...)
		}
		files["frontend/"+name] = data
	}
	if err := os.Mkdir(dest, 0o755); err != nil {
		return fmt.Errorf("meta init: create destination %q: %w", dest, err)
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(dest, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("meta init: incomplete project at %s: %w", dest, err)
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return fmt.Errorf("meta init: incomplete project at %s: %w", dest, err)
		}
		_, writeErr := file.Write(files[name])
		closeErr := file.Close()
		if writeErr != nil {
			return fmt.Errorf("meta init: incomplete project at %s: %w", dest, writeErr)
		}
		if closeErr != nil {
			return fmt.Errorf("meta init: incomplete project at %s: %w", dest, closeErr)
		}
	}
	fmt.Fprintf(ctx.Stdout, "Created meta project %s (module %s).\n\nNext:\n  cd %s\n  meta setup\n  meta dev\n", dest, modulePath, "'"+strings.ReplaceAll(dest, "'", "'\\''")+"'")
	if replacement != "" {
		fmt.Fprintln(ctx.Stdout, "Using a local framework checkout; remove the go.mod replace before publishing your application.")
	}
	return nil
}

func initFramework(dir string) (version, replacement string, err error) {
	if dir == "" {
		if releaseVersion(Version) {
			return Version, "", nil
		}
		if info, ok := debug.ReadBuildInfo(); ok {
			if version := releaseBuildVersion(info); version != "" {
				return version, "", nil
			}
		}
		cwd, e := os.Getwd()
		if e == nil {
			if root, e := findRootUpward(cwd); e == nil {
				if data, e := os.ReadFile(filepath.Join(root, "go.mod")); e == nil && modfile.ModulePath(data) == frameworkModule {
					dir = root
				}
			}
		}
		if dir == "" {
			return "", "", fmt.Errorf("meta init: development builds need --framework-dir pointing to a meta checkout")
		}
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", "", fmt.Errorf("meta init: framework directory: %w", err)
	}
	data, err := os.ReadFile(filepath.Join(abs, "go.mod"))
	if err != nil || modfile.ModulePath(data) != frameworkModule {
		return "", "", fmt.Errorf("meta init: %q is not a meta framework checkout", dir)
	}
	if info, err := os.Stat(filepath.Join(abs, "server", "server.go")); err != nil || info.IsDir() {
		return "", "", fmt.Errorf("meta init: %q does not contain the public runtime", dir)
	}
	return "v0.0.0", "replace " + frameworkModule + " => " + strconv.Quote(abs), nil
}

// Only published tags are safe defaults. Local builds may carry a valid-looking
// pseudo-version, including +dirty metadata, that cannot reproduce the binary.
func releaseVersion(version string) bool {
	return semver.IsValid(version) && !module.IsPseudoVersion(version) && semver.Build(version) == ""
}

func releaseBuildVersion(info *debug.BuildInfo) string {
	if info == nil || info.Main.Path != frameworkModule || !releaseVersion(info.Main.Version) {
		return ""
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.modified" && setting.Value == "true" {
			return ""
		}
	}
	return info.Main.Version
}

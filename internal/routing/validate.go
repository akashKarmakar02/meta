package routing

import "fmt"

// ValidateRoutes applies post-discovery collision rules to the discovered
// route set. B08 scope: duplicate normalized routes, which arise when one
// directory holds more than one kind of page file (page.templ and
// page.svelte). Layout conflicts and other validation rules arrive with
// later tasks.
func ValidateRoutes(routes []Route) error {
	byPath := map[string]Route{}
	for _, r := range routes {
		if prev, dup := byPath[r.Path]; dup {
			return fmt.Errorf(
				"routing: duplicate route %q from %q and %q: choose one page file per directory (page.templ or page.svelte), not both",
				r.Path, prev.SourceFile, r.SourceFile,
			)
		}
		byPath[r.Path] = r
	}
	for _, r := range routes {
		if err := validateLayoutChain(r); err != nil {
			return err
		}
	}
	return nil
}

// validateLayoutChain enforces the Phase 1 layout ownership rules
// (blueprint §4.3):
//
//   - An SSR layout (layout.templ) may wrap anything, including CSR
//     layouts and CSR pages: the root Go layout owns the document while a
//     nested Svelte layout owns its client DOM subtree.
//   - Once a chain contains a CSR layout, nothing deeper may be SSR: an
//     SSR layout or SSR page cannot render inside a client-only Svelte
//     subtree. That includes an SSR page sitting directly inside a
//     layout.svelte directory.
//
// Same-directory layout.templ + layout.svelte is already rejected during
// discovery (blueprint §3.4).
func validateLayoutChain(r Route) error {
	csrSeen := false
	for _, l := range r.Layouts {
		if l.Mode == ModeCSR {
			csrSeen = true
			continue
		}
		if csrSeen && l.Mode == ModeSSR {
			return fmt.Errorf(
				"routing: route %q: SSR layout %q cannot nest beneath CSR layout %q: server-rendered layouts cannot live inside a client Svelte subtree",
				r.Path, l.SourceFile, innermostCSR(r.Layouts),
			)
		}
	}
	if csrSeen && r.Mode == ModeSSR {
		return fmt.Errorf(
			"routing: route %q: SSR page %q cannot render beneath CSR layout %q: move the page into a directory without a layout.svelte ancestor, or make it page.svelte",
			r.Path, r.SourceFile, innermostCSR(r.Layouts),
		)
	}
	return nil
}

// innermostCSR returns the deepest CSR layout source path in the chain,
// for use in error messages.
func innermostCSR(layouts []Layout) string {
	deepest := ""
	for _, l := range layouts {
		if l.Mode == ModeCSR {
			deepest = l.SourceFile
		}
	}
	return deepest
}

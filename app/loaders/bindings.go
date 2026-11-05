// Package loaders holds explicit bindings between declared routes and loaders.
package loaders

import "github.com/akashKarmakar02/meta/loader"

// Bindings returns application loader bindings. Route IDs are page source
// paths relative to app/, such as "users/[userId]/page.templ".
//
// The c05/* routes are C05 fixtures: their loaders redirect or report an error
// outcome so the error/redirect/cancellation contract can be exercised
// end-to-end. staleSource has no binding (it is only a navigation source).
func Bindings() []loader.Binding {
	return []loader.Binding{
		loader.Bind("posts/[postId]/page.templ", LoadPost),
		loader.Bind("dashboard/[teamId]/page.svelte", LoadDashboard),
		loader.Bind("c05/redirectSsr/page.templ", LoadC05RedirectSSR),
		loader.Bind("c05/redirectCsr/page.svelte", LoadC05RedirectCSR),
		loader.Bind("c05/slowRedirect/page.templ", LoadC05SlowRedirect),
		loader.Bind("c05/notFound/page.templ", LoadC05NotFound),
		// C06 three-state fixture: JSON null (vs absent vs value).
		loader.Bind("c05/nullData/page.svelte", LoadC05NullData),
	}
}

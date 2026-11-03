// Package loaders holds explicit bindings between declared routes and loaders.
package loaders

import "go-meta/loader"

// Bindings returns application loader bindings. Route IDs are page source
// paths relative to app/, such as "users/[userId]/page.templ".
func Bindings() []loader.Binding { return nil }

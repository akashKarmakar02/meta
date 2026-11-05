//go:build !unix

package httpserver

// No portable nonblocking-open flag; the resolver still rejects special
// files after the open via its regular-file check.
const syscallO_NONBLOCK = 0

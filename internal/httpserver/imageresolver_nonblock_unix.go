//go:build unix

package httpserver

import "syscall"

const syscallO_NONBLOCK = syscall.O_NONBLOCK

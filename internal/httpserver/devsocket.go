// Private development socket helpers shared by the meta CLI and dev
// backends (A06 §11). The backend socket is a private channel between the
// public proxy and Fiber: it must live in a session-owned directory with
// restrictive permissions, and stale cleanup must never follow or remove
// anything that is not exactly our socket.
package httpserver

import "os"

// NewDevSessionDir creates a fresh session-private directory for the
// private backend Unix socket. 0700 keeps every other uid out, so
// unrelated local users can neither reach the private backend nor tamper
// with the socket file.
func NewDevSessionDir() (string, error) {
	dir, err := os.MkdirTemp("", "go-meta-dev-")
	if err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

// RemoveStaleSocket removes a leftover socket from a previous session so a
// fresh backend can bind. It refuses to touch anything that is not an
// actual Unix socket at the exact path given — never a regular file, a
// directory, or a symlink target (Lstat sees the link itself).
func RemoveStaleSocket(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // absent: nothing stale to clean
		}
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return nil // not a socket: not ours to delete
	}
	return os.Remove(path)
}

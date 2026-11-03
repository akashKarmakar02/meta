// Socket security tests (A06 §11): the private development socket lives in
// a session-owned, restrictive directory; stale cleanup cannot follow
// attacker-controlled paths or delete unrelated files.
package httpserver

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
)

// TestNewDevSessionDirIsPrivate verifies the session directory is created
// with 0700 so other users cannot reach the private socket.
func TestNewDevSessionDirIsPrivate(t *testing.T) {
	dir, err := NewDevSessionDir()
	if err != nil {
		t.Fatalf("NewDevSessionDir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("session dir mode = %o, want 0700", info.Mode().Perm())
	}
}

// TestRemoveStaleSocketOnlyRemovesSockets guards cleanup against deleting
// arbitrary user files: only an actual socket at the exact expected path is
// removed, and symlinks are never followed.
func TestRemoveStaleSocketOnlyRemovesSockets(t *testing.T) {
	base := t.TempDir()

	t.Run("regular file left alone", func(t *testing.T) {
		sock := filepath.Join(base, "not-a-socket")
		if err := os.WriteFile(sock, []byte("user data"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := RemoveStaleSocket(sock); err != nil {
			t.Fatalf("RemoveStaleSocket on regular file: %v", err)
		}
		if _, err := os.Stat(sock); err != nil {
			t.Errorf("regular file was removed: %v", err)
		}
	})

	t.Run("directory left alone", func(t *testing.T) {
		sock := filepath.Join(base, "a-directory")
		if err := os.Mkdir(sock, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := RemoveStaleSocket(sock); err != nil {
			t.Fatalf("RemoveStaleSocket on directory: %v", err)
		}
		if _, err := os.Stat(sock); err != nil {
			t.Errorf("directory was removed: %v", err)
		}
	})

	t.Run("symlink never followed", func(t *testing.T) {
		victim := filepath.Join(base, "victim")
		if err := os.WriteFile(victim, []byte("important"), 0o644); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(base, "socket-link")
		if err := os.Symlink(victim, link); err != nil {
			t.Fatal(err)
		}
		if err := RemoveStaleSocket(link); err != nil {
			t.Fatalf("RemoveStaleSocket on symlink: %v", err)
		}
		if _, err := os.Lstat(link); err != nil {
			t.Errorf("symlink itself was removed: %v", err)
		}
		if _, err := os.Stat(victim); err != nil {
			t.Errorf("symlink target was deleted through the link: %v", err)
		}
	})

	t.Run("real socket removed", func(t *testing.T) {
		dir := t.TempDir()
		sock := filepath.Join(dir, "dev.sock")
		l, err := net.Listen("unix", sock)
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		if err := RemoveStaleSocket(sock); err != nil {
			t.Fatalf("RemoveStaleSocket: %v", err)
		}
		if _, err := os.Lstat(sock); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("stale socket still present: %v", err)
		}
	})
}

// TestDevSessionsGetDistinctDirs proves two sessions cannot accidentally
// share a private socket directory.
func TestDevSessionsGetDistinctDirs(t *testing.T) {
	a, err := NewDevSessionDir()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(a)
	b, err := NewDevSessionDir()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(b)
	if a == b {
		t.Errorf("two sessions shared the same directory %q", a)
	}
}

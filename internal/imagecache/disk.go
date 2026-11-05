package imagecache

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/akashKarmakar02/meta/imageopt"
)

// relativeName maps a validated key and concrete format onto the
// root-relative entry path: "ab/v1-<64 hex>.<format>". Only digests from
// imageopt.CacheIdentity.Key pass validKey, so no path separator, dot
// segment, or user-controlled substring can reach the filesystem (§16).
func relativeName(key string, format imageopt.Format) (string, error) {
	if err := validKey(key); err != nil {
		return "", err
	}
	if _, err := format.MIME(); err != nil {
		return "", err
	}
	return shardOf(key) + "/" + key + "." + string(format), nil
}

// shardOf returns the two-hex-character directory for a key. Keys are
// "v1-<64 hex>"; the shard comes from the first two HEX digits after the
// prefix, so entries spread across the 256 shards instead of all landing
// in a "v1" directory.
func shardOf(key string) string { return key[3:5] }

// validKey enforces the fixed filename alphabet: a digest key is exactly
// "v1-" followed by 64 lowercase hex characters. Anything else — including
// empty strings, traversal fragments, separators, or mixed case — is
// rejected before it can influence a filesystem path.
func validKey(key string) error {
	if !strings.HasPrefix(key, "v1-") || len(key) != 3+64 {
		return fmt.Errorf("imagecache: invalid cache key")
	}
	for _, ch := range key[3:] {
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f') {
			return fmt.Errorf("imagecache: invalid cache key")
		}
	}
	return nil
}

// writeTemp creates a unique temporary file inside the destination shard
// (mandatory same-filesystem for rename), writes data fully, fsyncs, and
// closes. The unique name prevents two concurrent writers of the same key
// from interleaving into one file; both publish, the later rename wins,
// and both contents are complete (§33). The reserved tmp- prefix lets
// startup reconciliation reclaim crash leftovers.
func writeTemp(root *os.Root, shard string, format imageopt.Format, data []byte) (string, error) {
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return "", fmt.Errorf("imagecache: temp name: %w", err)
	}
	tmp := shard + "/" + tempPrefix + hex.EncodeToString(rnd[:]) + "." + string(format)
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, filePerm)
	if err != nil {
		return "", fmt.Errorf("imagecache: create temp: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = root.Remove(tmp)
		return "", fmt.Errorf("imagecache: write temp: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = root.Remove(tmp)
		return "", fmt.Errorf("imagecache: sync temp: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = root.Remove(tmp)
		return "", fmt.Errorf("imagecache: close temp: %w", err)
	}
	return tmp, nil
}

// readAllBounded reads at most limit bytes from an open descriptor.
func readAllBounded(f *os.File, limit int64) ([]byte, error) {
	if limit < 0 {
		return nil, fmt.Errorf("imagecache: negative read limit")
	}
	return io.ReadAll(io.LimitReader(f, limit))
}

// reconcile rebuilds accounting from the existing directory state at Open
// time (§31). It is also the startup self-test for writability.
func (c *Cache) reconcile() error {
	// Prove writability with a real create/rename/remove cycle: a
	// read-only filesystem or wrong-owner directory fails here at
	// startup, not on first publication after a long uptime.
	const probe = ".write-probe"
	if err := c.root.WriteFile(probe, []byte("x"), filePerm); err != nil {
		return fmt.Errorf("imagecache: cache dir not writable: %w", err)
	}
	if err := c.root.Remove(probe); err != nil {
		return fmt.Errorf("imagecache: cache dir not writable: %w", err)
	}

	for s := 0; s < shardCount; s++ {
		shard := fmt.Sprintf("%02x", s)
		dirents, err := c.readDirShard(shard)
		if err != nil {
			continue // absent shard: nothing to reconcile
		}
		for _, name := range dirents {
			rel := shard + "/" + name
			switch {
			case strings.HasPrefix(name, tempPrefix):
				// Abandoned temporary file from a crashed writer: it was
				// never published (publication renames it away), so
				// removing it cannot lose data.
				_ = c.root.Remove(rel)
			case validEntryName(name):
				key, _, ok := parseEntryName(name)
				if !ok {
					continue
				}
				st, err := c.root.Lstat(rel)
				if err != nil || !st.Mode().IsRegular() {
					continue // foreign object in our namespace: ignore, never serve
				}
				c.indexLocked(key, rel, st.Size())
			default:
				// Foreign file: ignored — never read, served, or deleted.
				// The operator owns the directory's other content.
			}
		}
	}
	c.evictLocked()
	return nil
}

// readDirShard lists one shard directory safely under the root.
func (c *Cache) readDirShard(shard string) ([]string, error) {
	f, err := c.root.Open(shard)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	dirents, err := f.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(dirents))
	for _, d := range dirents {
		names = append(names, d.Name())
	}
	return names, nil
}

// validEntryName checks the "<key>.<format>" pattern with a known format.
func validEntryName(name string) bool {
	_, _, ok := parseEntryName(name)
	return ok
}

// parseEntryName splits a final-entry filename into key and format. ok is
// false unless both halves validate independently.
func parseEntryName(name string) (key string, format imageopt.Format, ok bool) {
	ext := filepath.Ext(name)
	if len(ext) < 2 {
		return "", "", false
	}
	format = imageopt.Format(ext[1:])
	if _, err := format.MIME(); err != nil {
		return "", "", false
	}
	key = strings.TrimSuffix(name, ext)
	if validKey(key) != nil {
		return "", "", false
	}
	return key, format, true
}

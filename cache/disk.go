package cache

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// diskEnvelopeMagic prefixes every on-disk envelope: "GMET".
var diskEnvelopeMagic = [4]byte{'G', 'M', 'E', 'T'}

// diskEnvelopeVersion is the current envelope format version. A file whose
// version byte differs is foreign or from an incompatible release; readers
// treat it as corrupt (a miss) rather than guessing at its layout.
const diskEnvelopeVersion = 1

// envelope sizes: magic(4) + version(1) + reserved(3) + expiresAt(8) +
// valueLen(8) + crc(4).
const envelopeHeaderSize = 4 + 1 + 3 + 8 + 8
const envelopeTrailerSize = 4

// ErrInvalidDiskConfig reports a DiskConfig rejected by NewDiskStore (empty
// directory, negative limits, mutually inconsistent fields). It is a
// construction-time caller bug, mirroring ErrInvalidConfig for Memory.
var ErrInvalidDiskConfig = errors.New("cache: invalid disk configuration")

// DiskConfig shapes a Disk store. The zero config is invalid: a cache
// directory must be named explicitly — guessing a location would silently
// scatter files through the working directory.
type DiskConfig struct {
	// Dir is the directory holding cache files. It is created if missing,
	// including parents. Required non-empty. Relative paths resolve against
	// the process working directory; production callers should pass an
	// absolute path.
	Dir string

	// MaxEntryBytes bounds one entry's value size in bytes — exactly the value
	// bytes, not Memory's accounted formula (the disk charges no key or map
	// overhead; only the value and its envelope live on disk). Zero disables
	// the per-entry limit. An oversized Set fails with ErrEntryTooLarge and
	// stores nothing.
	MaxEntryBytes int64

	// SweepInterval starts a background janitor that removes expired and
	// corrupt files every interval, and must be positive when set. Zero (the
	// default) disables the janitor; expiry is then lazy (reads treat expired
	// files as misses) and physical reclamation happens only through explicit
	// Sweep calls. The janitor stops when Close is called.
	SweepInterval time.Duration

	// FileMode and DirMode are the permission bits for created files and
	// directories. Zero defaults to 0o600 / 0o700.
	FileMode fs.FileMode
	DirMode  fs.FileMode
}

func (c DiskConfig) validate() error {
	if strings.TrimSpace(c.Dir) == "" {
		return fmt.Errorf("%w: Dir is required", ErrInvalidDiskConfig)
	}
	if c.MaxEntryBytes < 0 {
		return fmt.Errorf("%w: MaxEntryBytes must be zero or positive, got %d", ErrInvalidDiskConfig, c.MaxEntryBytes)
	}
	if c.SweepInterval < 0 {
		return fmt.Errorf("%w: SweepInterval must be zero or positive, got %s", ErrInvalidDiskConfig, c.SweepInterval)
	}
	return nil
}

// diskEntry is the parsed envelope read back from disk.
type diskEntry struct {
	expiresAt time.Time
	value     []byte
}

// Disk is a filesystem-backed Store persisting entries as individual files.
// Each key maps to exactly one file path derived from SHA-256 of the key text:
//
//	<Dir>/objects/<hash[0:2]>/<hash>.bin
//
// The path is a pure function of the key, so a restart finds every entry
// without an index: nothing about a live process is required to interpret the
// directory. Two processes pointed at the same Dir share the cache safely for
// reads and last-writer-wins writes (each file is written atomically via
// rename), but invalidation and capacity are NOT coordinated between them;
// see the Multi-instance notes below.
//
// # Envelope format
//
//	magic "GMET" | version u8 | reserved 3B | expiresAtUnixNano i64 |
//	valueLen u64 | value bytes | crc32(value)
//
// The CRC covers the value bytes. A file failing magic, version, length, or
// CRC checks is treated as a miss by Get and removed by Sweep — a torn or
// foreign file never surfaces as data.
//
// # Expiry and reclamation
//
// Expiry is lazy on Get (an expired file is a miss and is unlinked when the
// reader is sure of the timestamp). Physical reclamation otherwise happens
// through the background janitor (DiskConfig.SweepInterval) or an explicit
// Sweep. There is no total-size bound: the directory grows with distinct keys
// until entries expire and are swept. Callers serving unbounded key spaces
// must sweep.
//
// # Concurrency
//
// All operations are safe for concurrent use. Reads open the file once;
// writers create a unique temporary file in the target directory and rename
// it into place, which the target platform makes atomic within a filesystem.
// A concurrent Set/Delete/Get race on one key resolves to either the old or
// the new complete entry — never a torn read.
//
// # Multi-instance behavior
//
// Pointing two servers at the same Dir shares cached values (useful on one
// host), but Delete and expiry in one process do not notify the other, and a
// restarted process sees whatever survived. Per-process Memory stores layered
// above (if an application composes them) may therefore serve stale data
// until their own TTLs lapse. Distributed invalidation is out of scope.
type Disk struct {
	dir         string
	maxEntry    int64
	fileMode    fs.FileMode
	dirMode     fs.FileMode
	janitor     *time.Ticker
	janitorDone chan struct{}
	closeOnce   sync.Once
	closeErr    error
	closedMu    sync.RWMutex
	closed      bool
	sweepMu     sync.Mutex
}

// Compile-time proof that Disk satisfies the contract.
var _ Store = (*Disk)(nil)

// NewDiskStore constructs a filesystem-backed Store rooted at cfg.Dir. It
// creates the directory tree if missing and validates the configuration; no
// partial store is returned on error. When SweepInterval is set, a janitor
// goroutine starts immediately and must be stopped with Close.
func NewDiskStore(cfg DiskConfig) (*Disk, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	d := &Disk{
		dir:         cfg.Dir,
		maxEntry:    cfg.MaxEntryBytes,
		fileMode:    cfg.FileMode,
		dirMode:     cfg.DirMode,
		janitorDone: make(chan struct{}),
	}
	if d.fileMode == 0 {
		d.fileMode = 0o600
	}
	if d.dirMode == 0 {
		d.dirMode = 0o700
	}
	if err := os.MkdirAll(d.objectsRoot(), d.dirMode); err != nil {
		return nil, fmt.Errorf("cache: create disk store directory: %w", err)
	}
	if cfg.SweepInterval > 0 {
		d.janitor = time.NewTicker(cfg.SweepInterval)
		go d.janitorLoop()
	}
	return d, nil
}

func (d *Disk) objectsRoot() string { return filepath.Join(d.dir, "objects") }

// pathFor maps a validated key to its file path. A pure function of the key
// text; see Disk for the layout.
func (d *Disk) pathFor(key Key) string {
	sum := sha256.Sum256([]byte(key))
	name := fmt.Sprintf("%x", sum)
	return filepath.Join(d.objectsRoot(), name[:2], name+".bin")
}

// Get implements Store. Error precedence matches Memory: nil context, canceled
// context, key validation, closed store, then the read itself. Expired and
// corrupt files are misses; an expired file is unlinked opportunistically
// (best effort — failure to unlink is still reported as a plain miss).
func (d *Disk) Get(ctx context.Context, key Key) (Entry, bool, error) {
	if ctx == nil {
		return Entry{}, false, ErrNilContext
	}
	if err := ctx.Err(); err != nil {
		return Entry{}, false, err
	}
	if err := key.Validate(); err != nil {
		return Entry{}, false, err
	}
	if !d.isOpen() {
		return Entry{}, false, ErrClosed
	}
	entry, ok, err := readEnvelope(d.pathFor(key))
	if err != nil {
		// A present-but-unreadable envelope (torn write, foreign format, CRC
		// mismatch) is a miss, never an error to the caller: the caller would
		// have to regenerate the value anyway, and failing the request over a
		// cache fault inverts the cache's purpose. Sweep reclaims the file.
		return Entry{}, false, nil
	}
	if !ok {
		return Entry{}, false, nil
	}
	if !time.Now().Before(entry.expiresAt) {
		_ = os.Remove(d.pathFor(key)) // lazy reclaim, best effort
		return Entry{}, false, nil
	}
	return Entry{Value: entry.value}, true, nil
}

// Set implements Store. Validation happens before any write, exactly as in
// Memory. The write lands in a temporary file in the target directory and is
// renamed into place, so a crash mid-write leaves at most an orphan temp file
// (removed by the next Sweep), never a torn entry.
func (d *Disk) Set(ctx context.Context, key Key, entry Entry, policy Policy) error {
	if ctx == nil {
		return ErrNilContext
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := key.Validate(); err != nil {
		return err
	}
	if err := policy.Validate(); err != nil {
		return err
	}
	if d.maxEntry > 0 && int64(len(entry.Value)) > d.maxEntry {
		return fmt.Errorf("%w: value is %d bytes, per-entry limit is %d", ErrEntryTooLarge, len(entry.Value), d.maxEntry)
	}
	if !d.isOpen() {
		return ErrClosed
	}
	expiresAt := time.Now().Add(policy.TTL)
	return writeAtomic(d.pathFor(key), encodeEnvelope(expiresAt, entry.Value), d.fileMode, d.dirMode)
}

// Delete implements Store. Removing a missing key is a successful no-op.
func (d *Disk) Delete(ctx context.Context, key Key) error {
	if ctx == nil {
		return ErrNilContext
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := key.Validate(); err != nil {
		return err
	}
	if !d.isOpen() {
		return ErrClosed
	}
	if err := os.Remove(d.pathFor(key)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("cache: delete %s: %w", d.pathFor(key), err)
	}
	return nil
}

// Sweep removes every expired or unreadable envelope under the store directory
// and returns the number of files removed. Errors removing individual files
// are skipped (a file another process holds open on Windows still counts as a
// miss; it will be retried next sweep), but directory-walk failures abort the
// sweep and are returned. Sweep may be called concurrently with any Store
// operation and with itself.
func (d *Disk) Sweep(ctx context.Context, now time.Time) (int, error) {
	if ctx == nil {
		return 0, ErrNilContext
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if !d.isOpen() {
		return 0, ErrClosed
	}
	d.sweepMu.Lock()
	defer d.sweepMu.Unlock()

	root := d.objectsRoot()
	var removed int
	err := filepath.WalkDir(root, func(path string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if de.IsDir() || !strings.HasSuffix(path, ".bin") {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		entry, ok, readErr := readEnvelope(path)
		if readErr != nil || !ok || !now.Before(entry.expiresAt) {
			if rmErr := os.Remove(path); rmErr == nil || errors.Is(rmErr, fs.ErrNotExist) {
				removed++
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return removed, fmt.Errorf("cache: sweep %s: %w", root, err)
	}
	return removed, nil
}

// Close stops the janitor (if any) and rejects later operations with
// ErrClosed. Cached files stay on disk — persistence across restarts is the
// point of this store — and a new NewDiskStore on the same Dir reads them
// back. Close is idempotent and concurrency-safe.
func (d *Disk) Close() error {
	d.closeOnce.Do(func() {
		d.closedMu.Lock()
		d.closed = true
		d.closedMu.Unlock()
		if d.janitor != nil {
			d.janitor.Stop()
			close(d.janitorDone)
		}
	})
	return nil
}

func (d *Disk) isOpen() bool {
	d.closedMu.RLock()
	defer d.closedMu.RUnlock()
	return !d.closed
}

func (d *Disk) janitorLoop() {
	for {
		select {
		case <-d.janitorDone:
			return
		case <-d.janitor.C:
			_, _ = d.Sweep(context.Background(), time.Now())
		}
	}
}

// encodeEnvelope serializes value with its expiry into the on-disk format.
func encodeEnvelope(expiresAt time.Time, value []byte) []byte {
	buf := make([]byte, envelopeHeaderSize+len(value)+envelopeTrailerSize)
	copy(buf[0:4], diskEnvelopeMagic[:])
	buf[4] = diskEnvelopeVersion
	// bytes 5..7 reserved, zero.
	binary.LittleEndian.PutUint64(buf[8:16], uint64(expiresAt.UnixNano()))
	binary.LittleEndian.PutUint64(buf[16:24], uint64(len(value)))
	copy(buf[envelopeHeaderSize:], value)
	crc := crc32.ChecksumIEEE(value)
	binary.LittleEndian.PutUint32(buf[envelopeHeaderSize+len(value):], crc)
	return buf
}

// readEnvelope parses and validates one envelope file. ok=false with nil err
// means the file was missing; ok=false with an error means it was present but
// unreadable or malformed — both are misses for callers, but Sweep removes
// the latter while leaving a clean miss alone.
func readEnvelope(path string) (diskEntry, bool, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return diskEntry{}, false, nil
	}
	if err != nil {
		return diskEntry{}, false, fmt.Errorf("cache: read %s: %w", path, err)
	}
	if len(raw) < envelopeHeaderSize+envelopeTrailerSize {
		return diskEntry{}, false, fmt.Errorf("cache: envelope %s too short (%d bytes)", path, len(raw))
	}
	if string(raw[0:4]) != string(diskEnvelopeMagic[:]) {
		return diskEntry{}, false, fmt.Errorf("cache: envelope %s has bad magic", path)
	}
	if raw[4] != diskEnvelopeVersion {
		return diskEntry{}, false, fmt.Errorf("cache: envelope %s has unsupported version %d", path, raw[4])
	}
	valueLen := binary.LittleEndian.Uint64(raw[16:24])
	if int64(valueLen) != int64(len(raw)-envelopeHeaderSize-envelopeTrailerSize) {
		return diskEntry{}, false, fmt.Errorf("cache: envelope %s length mismatch: header says %d, file holds %d", path, valueLen, len(raw)-envelopeHeaderSize-envelopeTrailerSize)
	}
	value := raw[envelopeHeaderSize : envelopeHeaderSize+int(valueLen)]
	wantCRC := binary.LittleEndian.Uint32(raw[envelopeHeaderSize+int(valueLen):])
	if crc32.ChecksumIEEE(value) != wantCRC {
		return diskEntry{}, false, fmt.Errorf("cache: envelope %s failed CRC check", path)
	}
	unixNano := int64(binary.LittleEndian.Uint64(raw[8:16]))
	return diskEntry{
		expiresAt: time.Unix(0, unixNano),
		value:     append([]byte(nil), value...), // private copy; raw escapes
	}, true, nil
}

// writeAtomic writes data to path via a unique temporary file in the same
// directory followed by rename, so readers observe either the previous
// complete entry or the new complete entry.
func writeAtomic(path string, data []byte, fileMode, dirMode fs.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("cache: create shard directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("cache: create temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("cache: write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("cache: close temp file: %w", err)
	}
	if err := os.Chmod(tmpName, fileMode); err != nil {
		cleanup()
		return fmt.Errorf("cache: chmod temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("cache: publish entry: %w", err)
	}
	return nil
}

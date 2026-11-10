package pagecache

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"sync"
)

// By default a different production binary cannot reuse a previous binary's
// pages. Apps deploying assets independently should supply an explicit BuildID
// covering the entire release. An unreadable executable safely disables reuse
// across restarts by using a process-local random ID.
var executableBuildID = sync.OnceValue(func() string {
	if name, err := os.Executable(); err == nil {
		if file, err := os.Open(name); err == nil {
			defer file.Close()
			hash := sha256.New()
			if _, err := io.Copy(hash, file); err == nil {
				return hex.EncodeToString(hash.Sum(nil))
			}
		}
	}
	var random [16]byte
	_, _ = rand.Read(random[:])
	return hex.EncodeToString(random[:])
})

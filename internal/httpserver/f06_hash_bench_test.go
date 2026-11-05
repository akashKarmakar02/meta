package httpserver

import (
	"fmt"
	"testing"
)

// BenchmarkF06SHA256 isolates hashing in memory; it excludes file I/O.
func BenchmarkF06SHA256(b *testing.B) {
	for _, size := range []int{1024, 256 << 10, 4 << 20, 32 << 20} {
		b.Run(fmt.Sprintf("bytes=%d", size), func(b *testing.B) {
			src := make([]byte, size)
			for i := range src {
				src[i] = byte(i * 31)
			}
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				_ = sourceVersion(src)
			}
		})
	}
}

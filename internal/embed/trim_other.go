//go:build !linux

package embed

// releaseFreed does nothing where glibc's malloc_trim is not available.
func releaseFreed() {}

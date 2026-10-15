package index

import "github.com/muralx/codedoc/internal/store"

// PickHashedKey exposes pickHashedKey to tests.
var PickHashedKey = pickHashedKey

// SetAfterSync sets the hook run between a sync and its resolution.
func SetAfterSync(fn func() error) { afterSync = fn }

// WithBulk returns o syncing in bulk FTS mode from minFiles extracted
// files, ending it with resume (nil for the default).
func WithBulk(o Options, minFiles int, resume func(*store.Store) error) Options {
	o.bulk = bulkTuning{minFiles: minFiles, resume: resume}
	return o
}

// SetAfterBatch sets the hook run after each committed write batch of a
// sync and returns a function removing it.
func SetAfterBatch(fn func() error) func() {
	afterBatch = fn
	return func() { afterBatch = nil }
}

// SetChunkFiles sets how many paths a round of Paths or EnsureContext
// writes and returns a function restoring the default.
func SetChunkFiles(n int) func() {
	old := chunkFiles
	chunkFiles = n
	return func() { chunkFiles = old }
}

// SetFullMinFiles sets the fewest files a pending resolution covering
// half the database resolves in full for, and returns a function
// restoring the default.
func SetFullMinFiles(n int) func() {
	old := fullMinFiles
	fullMinFiles = n
	return func() { fullMinFiles = old }
}

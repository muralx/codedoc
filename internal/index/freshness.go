package index

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/gitmeta"
	"github.com/muralx/codedoc/internal/store"
)

// Freshness is the outcome of a query-time freshness check (spec 7.8).
type Freshness struct {
	// Fresh is true when the index matches the working tree after the
	// check; false when more than MaxFiles files changed and the index was
	// left as it was.
	Fresh bool
	// Changed counts added plus content-changed files found by the check.
	Changed int
	// Branch, Commit and Dirty describe the live working tree.
	Branch, Commit string
	Dirty          int
	// Missing reports that the repo directory is gone: the check left
	// the index as it was (spec 15) and Fresh is false.
	Missing bool
	// Unindexed reports that the repo had no index when the check ran.
	Unindexed bool
	// Outdated counts the files the check found extracted with another
	// extractor version (see checkVersion), all counted in Changed.
	Outdated int
}

// Ensure brings one repo's index up to date before a query when at most
// opts.MaxFiles files were added or changed. A resolution an interrupted
// run left pending is finished first. After writing it resolves what
// the sync affected (see syncer.scope). It runs discovery again (so
// added files are found and deleted ones removed), stats every known
// file, and hashes those whose size or mtime moved, plus the racily clean
// ones (see racyFrom). Over the limit it changes nothing and reports
// Fresh false; a stale answer is still an answer. When there is something
// to write it takes the store's write lock and checks again under it, so
// concurrent processes apply each change once, one after the other.
// Finally, with the lock released, the vectors of what it re-extracted
// are embedded (see embedTouched): vector writes need no lock (see
// store.WriteLock), so a slow model never holds up another process.
//
// A repo whose directory is missing is reported Missing and its index
// kept: the repo may have moved, or its disk be unmounted.
func Ensure(s *store.Store, r *config.Repo, opts Options) (Freshness, error) {
	return EnsureContext(context.Background(), s, r, opts)
}

// EnsureContext is Ensure for long work that must stop when ctx ends:
// more than chunkFiles added, changed or removed files are written in
// rounds (see Paths), ctx being checked between rounds, before the final
// pass, whose Changed then counts what the first scan found.
func EnsureContext(ctx context.Context, s *store.Store, r *config.Repo, opts Options) (Freshness, error) {
	if err := finishPending(s, opts); err != nil {
		return Freshness{}, err
	}
	if info, err := os.Stat(r.Path); err != nil || !info.IsDir() {
		return Freshness{Missing: true}, nil
	}
	f, _, cs, write, err := plan(s, r, opts)
	if err != nil || !write {
		return f, err
	}
	changed, chunked := f.Changed, f.Changed+len(cs.gone) > chunkFiles
	if chunked {
		if err := filesChunked(ctx, s, r, opts, changedPaths(cs)); err != nil {
			return f, err
		}
	}
	f, sy, err := syncLocked(s, r, opts)
	if chunked {
		f.Changed = max(f.Changed, changed)
	}
	if err != nil || sy == nil {
		return f, err
	}
	return f, embedTouched(s, opts, sy)
}

// changedPaths lists the paths a change set writes or removes.
func changedPaths(cs changeSet) []string {
	out := make([]string, 0, len(cs.write)+len(cs.gone))
	for _, d := range cs.write {
		out = append(out, d.Path)
	}
	for _, f := range cs.gone {
		out = append(out, f.Path)
	}
	return out
}

// syncLocked plans again under the store's write lock and, when there is
// still something to write, syncs and resolves; sy is nil otherwise.
func syncLocked(s *store.Store, r *config.Repo, opts Options) (f Freshness, sy *syncer, err error) {
	unlock, err := s.WriteLock()
	if err != nil {
		return f, nil, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	f, st, cs, write, err := plan(s, r, opts)
	if err != nil || !write {
		return f, nil, err
	}
	sy, err = sync(s, r, opts, st, cs)
	if err != nil {
		return f, nil, err
	}
	if afterSync != nil {
		if err := afterSync(); err != nil {
			return f, nil, err
		}
	}
	return f, sy, resolvePending(s, opts)
}

// afterSync, when set (by tests), runs between a sync and the resolution
// it left pending, so a test can interrupt a run there.
var afterSync func() error

// plan runs the freshness scan and reports whether the index needs a
// write: files changed within the limit, or the head moved.
func plan(s *store.Store, r *config.Repo, opts Options) (Freshness, state, changeSet, bool, error) {
	st, err := loadState(s, r.Path)
	if err != nil {
		return Freshness{}, st, changeSet{}, false, err
	}
	f := Freshness{Branch: st.head.Branch, Commit: st.head.Commit, Unindexed: st.prev.IndexedAt == 0,
		Outdated: len(st.reseed)}
	if st.isRepo {
		dirty, err := gitmeta.DirtyFiles(r.Path)
		if err != nil {
			return Freshness{}, st, changeSet{}, false, err
		}
		f.Dirty = len(dirty)
	}
	cs, changed, err := scan(r.Path, opts.Exclude, r.FileLanguages(), st.rows, racyFrom(st.prev.IndexedAt))
	if err != nil {
		return Freshness{}, st, changeSet{}, false, err
	}
	f.Changed = changed
	if changed > opts.MaxFiles {
		return f, st, cs, false, nil
	}
	f.Fresh = true
	upToDate := st.prev.ID != 0 && st.prev.Branch == st.head.Branch && st.prev.Commit == st.head.Commit
	return f, st, cs, !upToDate || len(cs.write) > 0 || len(cs.gone) > 0, nil
}

// racyWindow is how close to the indexing time a stored mtime makes a
// file racily clean.
const racyWindow = 2 * time.Second

// racyFrom applies git's racy-timestamp rule: a file whose stored mtime
// (Unix nanoseconds) is at or after the repo's indexed_at (Unix seconds)
// minus racyWindow may have been rewritten after it was hashed without its
// size or mtime changing, so its stat alone cannot prove it unchanged.
// It returns the first such mtime.
func racyFrom(indexedAt int64) int64 {
	return time.Unix(indexedAt, 0).Add(-racyWindow).UnixNano()
}

// scan builds the change set of a freshness check: files whose stat
// differs from their row (or that are new) are read and hashed; changed
// counts the new ones and those whose content hash moved. Files whose
// stored mtime is at or after racy are hashed even when their stat
// matches. Rows of files no longer listed, no longer readable, or of a
// language langs leaves out (see allowed) are gone.
func scan(root string, exclude []string, langs map[string]bool, rows []store.File, racy int64) (changeSet, int, error) {
	paths, err := indexablePaths(root, exclude)
	if err != nil {
		return changeSet{}, 0, err
	}
	cs := changeSet{existing: byPath(rows)}
	listed := make(map[string]bool, len(paths))
	changed := 0
	for _, p := range paths {
		state, d, err := check(root, p, cs.existing, racy, langs)
		if err != nil {
			return changeSet{}, 0, err
		}
		listed[p] = state != fileGone
		if state == fileChanged {
			changed++
		}
		if state == fileChanged || state == fileTouched {
			cs.write = append(cs.write, d)
		}
	}
	for _, f := range rows {
		if !listed[f.Path] {
			cs.gone = append(cs.gone, f)
		}
	}
	return cs, changed, nil
}

// fileState is what a freshness check found for one listed path.
type fileState int

const (
	fileSame    fileState = iota // stat matches the stored row
	fileTouched                  // stat moved or was racy, content hash did not
	fileChanged                  // new file, or its content hash moved
	fileGone                     // unreadable, binary or too large now
)

// check classifies one listed path, reading and hashing it only when its
// stat differs from the stored row or its stored mtime is racy.
func check(root, p string, existing map[string]store.File, racy int64, langs map[string]bool) (fileState, Discovered, error) {
	old, known := existing[p]
	if known && old.Mtime < racy && allowed(langs, old.Lang) && statUnchanged(root, old) {
		return fileSame, Discovered{}, nil
	}
	d, ok, err := inspect(root, p, langs)
	switch {
	case isUnreadable(err):
		return fileGone, d, nil
	case err != nil:
		return fileGone, d, err
	case !ok:
		return fileGone, d, nil
	case known && old.Hash == d.Hash:
		return fileTouched, d, nil
	default:
		return fileChanged, d, nil
	}
}

// statUnchanged reports whether a stored file still has its recorded
// size and mtime.
func statUnchanged(root string, f store.File) bool {
	info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(f.Path)))
	if err != nil {
		return false
	}
	return info.Mode().IsRegular() && info.Size() == f.Size && info.ModTime().UnixNano() == f.Mtime
}

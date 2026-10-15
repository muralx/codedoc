package index

import (
	"errors"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/store"
)

// Files re-extracts the given repo-relative paths of one repo now, how
// ever many other files changed, and resolves what that leaves pending:
// the synchronous reindex after a doc write. A path whose file is gone
// loses its row. It holds the store's write lock while it writes; the
// vectors of the paths are embedded after it releases it (see
// embedTouched).
func Files(s *store.Store, r *config.Repo, opts Options, paths []string) error {
	sy, err := filesLocked(s, r, opts, paths, true)
	if err != nil {
		return err
	}
	return embedTouched(s, opts, sy)
}

// filesLocked re-extracts paths and, when resolve is set, resolves what
// is pending, under the write lock; otherwise the resolution is left
// pending for a later round (see filesChunked).
func filesLocked(s *store.Store, r *config.Repo, opts Options, paths []string, resolve bool) (sy *syncer, err error) {
	unlock, err := s.WriteLock()
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	st, err := loadState(s, r.Path)
	if err != nil {
		return nil, err
	}
	cs := changeSet{existing: byPath(st.rows)}
	for _, p := range paths {
		d, ok, err := inspect(r.Path, p, r.FileLanguages())
		if err != nil {
			return nil, err
		}
		if ok {
			cs.write = append(cs.write, d)
		} else if old, known := cs.existing[p]; known {
			cs.gone = append(cs.gone, old)
		}
	}
	if sy, err = sync(s, r, opts, st, cs); err != nil || !resolve {
		return sy, err
	}
	return sy, resolvePending(s, opts)
}

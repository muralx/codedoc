package deps

import (
	"errors"
	"io/fs"
	"os"
	"slices"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/embed"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/store"
)

// EmbedVectors embeds, in the deps.db at path, the documented symbols
// and module docs (the README included) of the indexed package versions
// that repos of a workspace use and that have no vector yet (deps spec
// section 8). A package version is immutable, so each is embedded once,
// by whichever workspace gets there first; the vectors are written in
// small transactions without the deps.db write lock, and the workspace
// lock is not held either. With limit above 0 and more missing vectors
// than limit nothing is embedded and they are counted as deferred. The
// database is checkpointed after a pass that wrote, so vector scans read
// it memory mapped.
func EmbedVectors(s *store.Store, path string, e embed.Embedder, repos []*config.Repo, limit int) (index.VecResult, error) {
	var res index.VecResult
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return res, nil
	}
	runtime, dev, err := usedPackages(s, repos)
	if err != nil || len(runtime)+len(dev) == 0 {
		return res, err
	}
	db, err := Open(path)
	if err != nil {
		return res, err
	}
	defer func() { _ = db.Close() }()
	if res, err = db.embed(e, runtime, limit); err != nil || res.Deferred > 0 || res.Unavailable {
		return res, err
	}
	more, err := db.embed(e, dev, limit)
	res.Symbols += more.Symbols
	res.Modules += more.Modules
	res.Passages += more.Passages
	res.Deferred, res.Unavailable = more.Deferred, more.Unavailable
	return res, err
}

// usedPackages returns the ids of the indexed package versions repos
// use, sorted: those some manifest declares for running the code, then
// those declared for development only.
func usedPackages(s *store.Store, repos []*config.Repo) (runtime, dev []string, err error) {
	devOnly := map[string]bool{}
	for _, r := range repos {
		row, err := s.RepoByPath(r.Path)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		uses, err := s.DepUses(row.ID)
		if err != nil {
			return nil, nil, err
		}
		for _, u := range uses {
			if u.State != store.DepIndexed || u.Version == "" {
				continue
			}
			only, seen := devOnly[u.PackageID()]
			devOnly[u.PackageID()] = u.Dev && (only || !seen)
		}
	}
	for id, only := range devOnly {
		if only {
			dev = append(dev, id)
		} else {
			runtime = append(runtime, id)
		}
	}
	slices.Sort(runtime)
	slices.Sort(dev)
	return runtime, dev, nil
}

// embed embeds the missing vectors of the package versions ids.
func (d *DB) embed(e embed.Embedder, ids []string, limit int) (index.VecResult, error) {
	if len(ids) == 0 {
		return index.VecResult{}, nil
	}
	rows, err := d.RepoIDs(ids)
	if err != nil {
		return index.VecResult{}, err
	}
	repoIDs := make([]int64, 0, len(rows))
	for _, id := range ids {
		if rows[id] != 0 {
			repoIDs = append(repoIDs, rows[id])
		}
	}
	res, err := index.EmbedAPIVectors(d.s, e, repoIDs, limit)
	if err != nil || res.Symbols+res.Modules == 0 {
		return res, err
	}
	return res, d.s.Checkpoint()
}

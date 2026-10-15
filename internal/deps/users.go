package deps

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"github.com/muralx/codedoc/internal/store"
)

// heal marks pending the indexed uses of the repos ids whose package
// version deps.db no longer holds: it was pruned by a run that did not
// know this workspace, or deps.db was deleted. The Sync that calls it
// indexes them again within its deadline, and what is left counts as
// "deps: N pending" (deps spec section 9).
func heal(s *store.Store, ids map[string]int64, dbPath string) (err error) {
	gone, err := vanished(s, ids, dbPath)
	if err != nil || len(gone) == 0 {
		return err
	}
	unlock, err := s.WriteLock()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	return s.Batch(func(b *store.Batch) error {
		for _, id := range gone {
			if err := b.SetDepState(id.Eco, id.Name, id.Version, store.DepPending, ""); err != nil {
				return err
			}
		}
		return nil
	})
}

// vanished returns the package versions the repos ids use as indexed
// that deps.db does not hold.
func vanished(s *store.Store, ids map[string]int64, dbPath string) ([]ID, error) {
	indexed := map[string]bool{}
	for _, repoID := range ids {
		have, err := s.DepPackageIDs(repoID, store.DepIndexed)
		if err != nil {
			return nil, err
		}
		for _, id := range have {
			indexed[id] = true
		}
	}
	if len(indexed) == 0 {
		return nil, nil
	}
	db, err := Open(dbPath)
	if err != nil {
		return nil, err
	}
	present, err := db.RepoIDs(sortedKeys(indexed))
	if err = errors.Join(err, db.Close()); err != nil {
		return nil, err
	}
	var gone []ID
	for _, name := range sortedKeys(indexed) {
		if id, ok := ParseID(name); ok && present[name] == 0 {
			gone = append(gone, id)
		}
	}
	return gone, nil
}

// noteUsers records in deps.db the package versions the workspace
// database s uses, under the deps.db write lock: codedoc deps prune
// keeps them even when run with a config that does not list the
// workspace. A sync that changed nothing writes nothing, unless refresh
// is set (a full sync renews the time the uses were last seen).
func noteUsers(s *store.Store, dbPath string, refresh bool) error {
	wsDB, err := filepath.Abs(s.Path())
	if err != nil {
		return err
	}
	used, err := s.UsedDepPackageIDs()
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(dbPath); errors.Is(statErr, fs.ErrNotExist) && len(used) == 0 {
		return nil
	}
	db, err := Open(dbPath)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	recorded, err := db.s.DepUsers(wsDB)
	if err != nil {
		return err
	}
	have := make([]string, len(recorded))
	for i, u := range recorded {
		have[i] = u.Package
	}
	if slices.Equal(have, used) && (!refresh || len(used) == 0) {
		return nil
	}
	unlock, err := db.lock()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	return db.s.Batch(func(b *store.Batch) error { return b.ReplaceDepUsers(wsDB, used, time.Now().Unix()) })
}

// PruneOptions control one Prune.
type PruneOptions struct {
	// All empties the database whoever uses it.
	All bool
	// DryRun reports what would be pruned and deletes nothing.
	DryRun bool
	// Keep, when set, returns more package ids to keep: those of the
	// workspaces of the config in use. It runs under the write lock.
	Keep func() (map[string]bool, error)
}

// Pruned is the outcome of a Prune.
type Pruned struct {
	// Packages are the ids deleted (with DryRun: that would be), sorted.
	Packages []string
	// Users are the workspace databases recorded in deps.db whose
	// packages were kept; Gone those whose file no longer exists, which
	// are forgotten.
	Users, Gone []string
}

// Prune deletes every package version no workspace uses. The users are
// computed under the deps.db write lock, so a sync cannot index a
// package between the decision and the deletion: every workspace
// database recorded as a user (see noteUsers) is asked for its current
// uses, or trusted for the recorded ones when it cannot be opened; one
// whose file is gone is forgotten; opts.Keep adds the workspaces of the
// config. A package of none of them is deleted with its meta keys.
func (d *DB) Prune(opts PruneOptions) (out Pruned, err error) {
	unlock, err := d.lock()
	if err != nil {
		return out, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	keep := map[string]bool{}
	if !opts.All {
		if keep, err = d.inUse(&out, opts); err != nil {
			return out, err
		}
	}
	repos, err := d.s.Repos()
	if err != nil {
		return out, err
	}
	for _, r := range repos {
		if keep[r.Name] {
			continue
		}
		if !opts.DryRun {
			if err := d.remove(r); err != nil {
				return out, err
			}
		}
		out.Packages = append(out.Packages, r.Name)
	}
	sort.Strings(out.Packages)
	return out, nil
}

// inUse returns the package ids to keep (see Prune) and fills the users
// of out.
func (d *DB) inUse(out *Pruned, opts PruneOptions) (map[string]bool, error) {
	keep := map[string]bool{}
	if opts.Keep != nil {
		more, err := opts.Keep()
		if err != nil {
			return nil, err
		}
		for id := range more {
			keep[id] = true
		}
	}
	recorded, err := d.s.DepUsers("")
	if err != nil {
		return nil, err
	}
	byDB := map[string][]string{}
	for _, u := range recorded {
		byDB[u.WorkspaceDB] = append(byDB[u.WorkspaceDB], u.Package)
	}
	for _, wsDB := range sortedKeys(byDB) {
		ids := byDB[wsDB]
		if _, err := os.Stat(wsDB); errors.Is(err, fs.ErrNotExist) {
			out.Gone = append(out.Gone, wsDB)
			if !opts.DryRun {
				if err := d.s.Batch(func(b *store.Batch) error { return b.ReplaceDepUsers(wsDB, nil, 0) }); err != nil {
					return nil, err
				}
			}
			continue
		}
		if current, err := usedBy(wsDB); err == nil {
			ids = current
		}
		for _, id := range ids {
			keep[id] = true
		}
		out.Users = append(out.Users, wsDB)
	}
	return keep, nil
}

// usedBy reads the package ids a workspace database uses now.
func usedBy(wsDB string) ([]string, error) {
	s, err := store.Open(wsDB)
	if err != nil {
		return nil, err
	}
	ids, err := s.UsedDepPackageIDs()
	return ids, errors.Join(err, s.Close())
}

// remove deletes the rows and meta keys of one package version.
func (d *DB) remove(r store.Repo) error {
	return d.s.Batch(func(b *store.Batch) error {
		for _, key := range packageKeys {
			if err := b.DeleteMeta(key + r.Name); err != nil {
				return err
			}
		}
		return b.DeleteRepo(r.ID)
	})
}

package deps

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/store"
)

// Options control one Sync.
type Options struct {
	// DB is the path of deps.db (config.Config.DepsDBPath).
	DB string
	// Full makes Sync discover whether or not a manifest or lock file
	// changed, retry failed packages and check that the indexed ones are
	// still in deps.db: what codedoc index, codedoc deps index and the
	// daemon's catch-up run. Without it discovery runs only for a repo
	// whose tracked files changed (deps spec section 9).
	Full bool
	// Deadline, when set, is when Sync stops starting packages; those
	// left stay pending. The query path sets it two seconds ahead.
	Deadline time.Time
	// Limits bound each package; the zero value means DefaultLimits.
	Limits Limits
}

// Summary is the outcome of a Sync over some repos of a workspace.
type Summary struct {
	// Indexed counts the package versions this Sync wrote to deps.db;
	// Failed those whose extraction failed in it.
	Indexed, Failed int
	// Pending counts the distinct package versions of the repos still
	// waiting to be indexed.
	Pending int
	// Problems are manifests that could not be read.
	Problems []string
}

// relinks is what a sync learned about the edges into dependencies it
// leaves to resolve again (see relink).
type relinks struct {
	// all is set when the workspace was synced against this deps.db for
	// the first time: every file importing a package is resolved again.
	all bool
	// rewritten holds the ids of the packages the sync wrote to deps.db.
	rewritten map[string]bool
}

// Sync brings the dependency uses of repos of a workspace up to date and
// indexes the package versions deps.db lacks. Discovery runs per repo
// when a tracked file changed (see Options.Full) and replaces the repo's
// uses under the workspace write lock, which is released before any
// package is extracted; each package is then written under the deps.db
// lock alone, and the outcomes are recorded under the workspace lock
// again. An indexed use whose package deps.db no longer holds becomes
// pending first (see heal), and the package versions the workspace uses
// are recorded in deps.db for codedoc deps prune (see noteUsers). A repo
// whose directory is gone or that was never indexed is left as it is. With deps.enabled false the uses are cleared. Last, the
// references of the files importing a package whose indexed version
// changed, or whose rows this Sync wrote, are resolved again, so the
// edges from workspace code follow the version in use (see relink).
func Sync(ctx context.Context, s *store.Store, ws *config.Workspace, repos []*config.Repo, opts Options) (Summary, error) {
	ids, err := repoIDs(s, repos)
	if err != nil {
		return Summary{}, err
	}
	before, err := indexedVersions(s, ids)
	if err != nil {
		return Summary{}, err
	}
	again := relinks{rewritten: map[string]bool{}}
	sum, err := syncUses(ctx, s, ws, repos, ids, opts, &again)
	if err != nil {
		return sum, err
	}
	return sum, relink(s, ws, ids, before, again)
}

// syncUses is Sync without the resolution that follows it; again
// receives what that resolution has to cover.
func syncUses(ctx context.Context, s *store.Store, ws *config.Workspace, repos []*config.Repo, ids map[string]int64,
	opts Options, again *relinks) (Summary, error) {
	var sum Summary
	var err error
	if !ws.Deps.On() {
		if err := clearUses(s, ids); err != nil {
			return sum, err
		}
		return sum, noteUsers(s, opts.DB, false)
	}
	if again.all, err = noteDB(s, opts.DB); err != nil {
		return sum, err
	}
	v := newView(s, ws)
	if sum.Problems, err = v.refreshAll(repos, ids, opts.Full); err != nil {
		return sum, err
	}
	if err := v.applyUsage(ids); err != nil {
		return sum, err
	}
	if err := heal(s, ids, opts.DB); err != nil {
		return sum, err
	}
	if err := indexTodo(ctx, s, ids, opts, v.only.only, &sum, again); err != nil {
		return sum, err
	}
	if err := noteUsers(s, opts.DB, opts.Full); err != nil {
		return sum, err
	}
	sum.Pending, err = Pending(s, repos)
	return sum, err
}

// refreshAll runs refresh for every repo that has a row and returns the
// manifests that could not be read.
func (v *view) refreshAll(repos []*config.Repo, ids map[string]int64, full bool) (problems []string, err error) {
	for _, r := range repos {
		id, ok := ids[r.Name]
		if !ok {
			continue
		}
		more, err := v.refresh(r, id, full)
		if err != nil {
			return problems, err
		}
		problems = append(problems, more...)
	}
	return problems, nil
}

// indexTodo indexes the package versions that need work (see
// candidates) and records the outcomes; only names the modules wanted
// of the packages indexed partially.
func indexTodo(ctx context.Context, s *store.Store, ids map[string]int64, opts Options, only map[string][]string,
	sum *Summary, again *relinks) error {
	todo, err := candidates(s, ids, opts.Full)
	if err != nil || len(todo) == 0 {
		return err
	}
	outcomes, err := indexPackages(ctx, todo, opts, only)
	if err != nil {
		return err
	}
	return record(s, outcomes, sum, again)
}

// repoIDs returns the row ids of the repos that exist on disk and in
// the store, by name.
func repoIDs(s *store.Store, repos []*config.Repo) (map[string]int64, error) {
	out := map[string]int64{}
	for _, r := range repos {
		if info, err := os.Stat(r.Path); err != nil || !info.IsDir() {
			continue
		}
		row, err := s.RepoByPath(r.Path)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out[r.Name] = row.ID
	}
	return out, nil
}

// Pending counts the distinct package versions that repos use and that
// wait to be indexed: what a query reports as "deps: N pending".
func Pending(s *store.Store, repos []*config.Repo) (int, error) {
	seen := map[string]bool{}
	for _, r := range repos {
		row, err := s.RepoByPath(r.Path)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return 0, err
		}
		ids, err := s.DepPackageIDs(row.ID, store.DepPending)
		if err != nil {
			return 0, err
		}
		for _, id := range ids {
			seen[id] = true
		}
	}
	return len(seen), nil
}

// PackageIDs returns the ids of the indexed package versions a repo of
// the workspace uses, or the whole workspace when repoID is 0, sorted:
// the versions a query with --deps searches in deps.db.
func PackageIDs(s *store.Store, repoID int64) ([]string, error) {
	return s.DepPackageIDs(repoID, store.DepIndexed)
}

// clearUses removes the dependency uses and tracked files of repos.
func clearUses(s *store.Store, ids map[string]int64) error {
	for _, id := range ids {
		uses, err := s.DepUses(id)
		if err != nil {
			return err
		}
		tracked, err := s.DepManifests(id)
		if err != nil {
			return err
		}
		if len(uses) == 0 && len(tracked) == 0 {
			continue
		}
		if err := writeUses(s, id, nil, nil); err != nil {
			return err
		}
	}
	return nil
}

// refresh runs discovery for one repo when full is set or a tracked
// file changed, and stores the result.
func (v *view) refresh(r *config.Repo, repoID int64, full bool) (problems []string, err error) {
	manifests, err := manifestsOf(v.s, repoID)
	if err != nil {
		return nil, err
	}
	stored, err := v.s.DepManifests(repoID)
	if err != nil {
		return nil, err
	}
	old, err := v.s.DepUses(repoID)
	if err != nil {
		return nil, err
	}
	now, changed := snapshot(r.Path, trackedPaths(manifests), stored)
	if !full && !changed && !appeared(old) && !hasLegacyLocal(old) {
		return nil, nil
	}
	return v.rediscover(r, repoID, manifests, old, now, stored)
}

// rediscover runs discovery for one repo and stores the uses, each
// keeping the state its package version already has in the workspace,
// or unused when the code does not import it (see mark).
func (v *view) rediscover(r *config.Repo, repoID int64, manifests map[string]Ecosystem, old []store.DepUse,
	now, stored []store.DepManifest) (problems []string, err error) {
	uses, problems := v.discover(r.Path, manifests)
	known, err := v.knownStates()
	if err != nil {
		return nil, err
	}
	for i, u := range uses {
		if prev, ok := known[u.PackageID()]; ok && u.State == store.DepPending {
			uses[i].State, uses[i].Error = prev.State, prev.Error
		}
	}
	if err := v.mark(repoID, uses); err != nil {
		return nil, err
	}
	if len(uses)+len(old)+len(now)+len(stored) == 0 {
		return problems, nil // a repo without manifests has nothing to record
	}
	return problems, writeUses(v.s, repoID, uses, now)
}

// knownStates returns, by package id, an indexed or failed use of the
// workspace: the state a rediscovered use of the same version keeps.
func (v *view) knownStates() (map[string]store.DepUse, error) {
	all, err := v.s.DepUses(0)
	if err != nil {
		return nil, err
	}
	out := map[string]store.DepUse{}
	for _, u := range all {
		if u.State == store.DepIndexed || u.State == store.DepFailed {
			out[u.PackageID()] = u
		}
	}
	return out, nil
}

// writeUses replaces the uses and tracked files of a repo under the
// workspace write lock.
func writeUses(s *store.Store, repoID int64, uses []store.DepUse, tracked []store.DepManifest) (err error) {
	unlock, err := s.WriteLock()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	return s.Batch(func(b *store.Batch) error {
		if err := b.ReplaceDepUses(repoID, uses); err != nil {
			return err
		}
		return b.ReplaceDepManifests(repoID, tracked)
	})
}

// ResetStates marks every indexed use of a workspace pending again,
// under its write lock: deps.db was emptied, so the next Sync indexes
// the packages again.
func ResetStates(s *store.Store) (err error) {
	unlock, err := s.WriteLock()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	return s.Batch(func(b *store.Batch) error { return b.ResetDepStates() })
}

// noteDB records in the workspace database which deps.db its uses are
// synced against, under the write lock, when that changed: reference
// resolution reads the packages from there (store.DepsDBKey). It reports
// whether it changed, the first sync included: the edges into
// dependencies were then resolved against no deps.db or another one.
func noteDB(s *store.Store, path string) (changed bool, err error) {
	if have, _, err := s.Meta(store.DepsDBKey); err != nil || have == path {
		return false, err
	}
	unlock, err := s.WriteLock()
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	return true, s.Batch(func(b *store.Batch) error { return b.SetMeta(store.DepsDBKey, path) })
}

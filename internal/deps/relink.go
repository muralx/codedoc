package deps

import (
	"errors"
	"sort"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/store"
)

// pinned is the package version one manifest resolves a package to: the
// declared name, which import specifiers start with, and the package id.
type pinned struct{ name, id string }

// versions maps a repo row to what each of its manifests resolves each
// package to, for the indexed ones: what the edges from workspace code
// into dependencies were resolved against.
type versions map[int64]map[string]pinned

// indexedVersions reads the indexed uses of the repos ids.
func indexedVersions(s *store.Store, ids map[string]int64) (versions, error) {
	out := versions{}
	for _, repoID := range ids {
		uses, err := s.DepUses(repoID)
		if err != nil {
			return nil, err
		}
		out[repoID] = map[string]pinned{}
		for _, u := range uses {
			if u.State == store.DepIndexed {
				out[repoID][u.Manifest+"\x00"+u.Eco+"\x00"+u.Name] = pinned{u.Name, u.PackageID()}
			}
		}
	}
	return out, nil
}

// moved returns, per repo, the names of the packages some manifest
// resolves to another indexed version than before, or stopped or started
// resolving to one, and those of rewritten the repo uses, sorted.
func (before versions) moved(after versions, rewritten map[string]bool) map[int64][]string {
	out := map[int64][]string{}
	for repoID := range after {
		names := map[string]bool{}
		diff := func(a, b map[string]pinned) {
			for k, v := range a {
				if b[k] != v || rewritten[v.id] {
					names[v.name] = true
				}
			}
		}
		diff(after[repoID], before[repoID])
		diff(before[repoID], after[repoID])
		if len(names) > 0 {
			out[repoID] = sortedKeys(names)
		}
	}
	return out
}

// relink resolves again the references of the files importing a package
// whose indexed version changed since before (deps spec section 6): the
// workspace moved to another version, a version was just indexed or
// written again by a newer extraction, or the package is no longer used;
// every package, when the workspace is synced against this deps.db for
// the first time (a workspace indexed before dependencies had edges). The files are queued and resolved like any
// pending resolution, under the workspace write lock, which nothing
// holds while a package is extracted.
func relink(s *store.Store, ws *config.Workspace, ids map[string]int64, before versions, again relinks) error {
	after, err := indexedVersions(s, ids)
	if err != nil {
		return err
	}
	if again.all {
		before = versions{}
	}
	var files []int64
	moved := before.moved(after, again.rewritten)
	repoIDs := make([]int64, 0, len(moved))
	for id := range moved {
		repoIDs = append(repoIDs, id)
	}
	sort.Slice(repoIDs, func(i, j int) bool { return repoIDs[i] < repoIDs[j] })
	for _, repoID := range repoIDs {
		importing, err := s.FilesImporting(repoID, moved[repoID])
		if err != nil {
			return err
		}
		files = append(files, importing...)
	}
	if len(files) == 0 {
		return nil
	}
	if err := queue(s, files); err != nil {
		return err
	}
	return index.ResolvePending(s, index.Options{Workspace: ws.Name, RepoOrder: ws.RepoNames()})
}

// queue adds files to the pending resolution of the workspace under its
// write lock.
func queue(s *store.Store, files []int64) (err error) {
	unlock, err := s.WriteLock()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	return s.Batch(func(b *store.Batch) error { return index.QueueResolution(b, files, nil) })
}

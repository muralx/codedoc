package index

import (
	"sort"
	"strings"

	"github.com/muralx/codedoc/internal/parse"
	"github.com/muralx/codedoc/internal/resolve"
	"github.com/muralx/codedoc/internal/store"
)

// moduleState is what the module step of a sync decided.
type moduleState struct {
	ids     *resolve.Map
	rowIDs  map[string]int64 // lang + "\x00" + path -> module row id
	stale   []int64          // module rows no longer backed by a directory
	moved   map[string]bool  // directories (lang + "\x00" + path) whose identity changed or is new to stored files
	changed bool             // any module or identity was added, removed or renamed
}

// moduleOf returns the module of a file and its row id.
func (ms *moduleState) moduleOf(lang, filePath string) (resolve.Module, int64, bool) {
	m, ok := ms.ids.ModuleFor(lang, filePath)
	return m, ms.rowIDs[m.Lang+"\x00"+m.Path], ok
}

// prepareModules computes the identity map from the files the repo holds
// after the change set, and stores the repo identities and module rows
// before any file is written, so symbols get their module ids and keys.
func (sy *syncer) prepareModules(cs changeSet) (*moduleState, error) {
	ids, err := resolve.Identities(sy.root, filesAfter(cs))
	if err != nil {
		return nil, err
	}
	old, err := sy.s.ModulesByRepo(sy.repoID)
	if err != nil {
		return nil, err
	}
	oldIDs, err := sy.s.RepoIdentities(sy.repoID)
	if err != nil {
		return nil, err
	}
	ms := &moduleState{ids: ids, rowIDs: map[string]int64{}, moved: map[string]bool{}}
	ms.diff(old, oldIDs)
	err = sy.s.Batch(func(b *store.Batch) error {
		if err := b.ReplaceRepoIdentities(sy.repoID, repoIdentities(ids.Repo)); err != nil {
			return err
		}
		return ms.upsert(b, sy.repoID)
	})
	return ms, err
}

// diff compares the new map with the stored modules and identities. A
// module moves when its identity, kind, package root or package name
// changed: each changes the qualified names or stable keys of its symbols.
func (ms *moduleState) diff(old []store.Module, oldIDs []store.RepoIdentity) {
	byKey := make(map[string]store.Module, len(old))
	for _, m := range old {
		byKey[m.Lang+"\x00"+m.Path] = m
	}
	roots := storedRoots(old)
	for _, m := range ms.ids.Modules {
		key := m.Lang + "\x00" + m.Path
		o, ok := byKey[key]
		if !ok || o.Identity != m.Identity || o.Kind != m.Kind || roots[o.ID] != [2]string{m.Root, m.Package} {
			ms.moved[key], ms.changed = true, true
		}
		delete(byKey, key)
	}
	for _, o := range byKey {
		ms.stale = append(ms.stale, o.ID)
	}
	sort.Slice(ms.stale, func(i, j int) bool { return ms.stale[i] < ms.stale[j] })
	ms.changed = ms.changed || len(ms.stale) > 0 || !sameIdentities(oldIDs, repoIdentities(ms.ids.Repo))
}

// storedRoots derives the package root and package name of stored
// modules (see resolve.Module): an npm package's own path and identity,
// shared by the npm-dir modules below it through their parent links; the
// repo root and the module's identity for every other kind.
func storedRoots(mods []store.Module) map[int64][2]string {
	byID := make(map[int64]store.Module, len(mods))
	for _, m := range mods {
		byID[m.ID] = m
	}
	out := make(map[int64][2]string, len(mods))
	for _, m := range mods {
		top := m
		for top.Kind == "npm-dir" && top.ParentID != 0 {
			top = byID[top.ParentID]
		}
		if top.Kind == "npm-package" {
			out[m.ID] = [2]string{top.Path, top.Identity}
		} else {
			out[m.ID] = [2]string{".", m.Identity}
		}
	}
	return out
}

// sameIdentities reports whether two identity lists hold the same rows,
// in any order.
func sameIdentities(a, b []store.RepoIdentity) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[store.RepoIdentity]bool, len(a))
	for _, id := range a {
		set[id] = true
	}
	for _, id := range b {
		if !set[id] {
			return false
		}
	}
	return true
}

// upsert writes the module rows, parents before children.
func (ms *moduleState) upsert(b *store.Batch, repoID int64) error {
	mods := append([]resolve.Module(nil), ms.ids.Modules...)
	sort.SliceStable(mods, func(i, j int) bool { return depth(mods[i].Path) < depth(mods[j].Path) })
	for _, m := range mods {
		var parent int64
		if m.Parent != "" {
			parent = ms.rowIDs[m.Lang+"\x00"+m.Parent]
		}
		id, err := b.UpsertModule(store.Module{RepoID: repoID, Path: m.Path, Lang: m.Lang, Kind: m.Kind,
			Identity: m.Identity, ParentID: parent})
		if err != nil {
			return err
		}
		ms.rowIDs[m.Lang+"\x00"+m.Path] = id
	}
	return nil
}

func depth(p string) int {
	if p == "." {
		return 0
	}
	return strings.Count(p, "/") + 1
}

func repoIdentities(ids []resolve.Identity) []store.RepoIdentity {
	out := make([]store.RepoIdentity, len(ids))
	for i, id := range ids {
		out[i] = store.RepoIdentity{Lang: id.Lang, Identity: id.Identity, Path: id.Path}
	}
	return out
}

// filesAfter lists the repo-relative paths the repo holds once cs is
// applied: stored files that are not gone, plus written ones.
func filesAfter(cs changeSet) []string {
	gone := make(map[string]bool, len(cs.gone))
	for _, f := range cs.gone {
		gone[f.Path] = true
	}
	seen := make(map[string]bool, len(cs.existing)+len(cs.write))
	var out []string
	add := func(p string) {
		if !gone[p] && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for p := range cs.existing {
		add(p)
	}
	for _, d := range cs.write {
		add(d.Path)
	}
	sort.Strings(out)
	return out
}

// withMovedModules adds the stored files, not already written or gone,
// whose module identity changed: their stable keys must be rebuilt.
func withMovedModules(cs changeSet, ms *moduleState, write []Discovered) ([]Discovered, map[string]bool) {
	inSet := make(map[string]bool, len(write)+len(cs.gone))
	for _, d := range write {
		inSet[d.Path] = true
	}
	for _, f := range cs.gone {
		inSet[f.Path] = true
	}
	force := map[string]bool{}
	for p, f := range cs.existing {
		m, ok := ms.ids.ModuleFor(parse.Family(f.Lang), p)
		if !ok || !ms.moved[m.Lang+"\x00"+m.Path] {
			continue
		}
		force[p] = true
		if !inSet[p] {
			write = append(write, fromRow(f))
		}
	}
	sort.SliceStable(write, func(i, j int) bool { return write[i].Path < write[j].Path })
	return write, force
}

// fromRow rebuilds the discovery record of a stored, unchanged file.
func fromRow(f store.File) Discovered {
	return Discovered{Path: f.Path, Lang: f.Lang, Size: f.Size, Mtime: f.Mtime, Hash: f.Hash,
		Lines: f.Lines, Generated: f.Generated}
}

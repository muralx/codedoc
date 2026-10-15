package deps

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/store"
)

// view is what discovery knows of the workspace around a repo.
type view struct {
	s  *store.Store
	ws *config.Workspace
	// roots are the real paths of the workspace's repos.
	roots []string
	// provided caches Provided answers by lang and name.
	provided map[[2]string]bool
	// imports caches what the code of each repo imports, by repo row.
	imports map[int64]imports
	// only collects the modules wanted of the packages indexed
	// partially (see mark).
	only wants
}

func newView(s *store.Store, ws *config.Workspace) *view {
	v := &view{s: s, ws: ws, provided: map[[2]string]bool{}, imports: map[int64]imports{},
		only: wants{only: map[string][]string{}, whole: map[string]bool{}}}
	for _, r := range ws.Repos {
		root := r.Path
		if resolved, err := filepath.EvalSymlinks(root); err == nil {
			root = resolved
		}
		v.roots = append(v.roots, root)
	}
	return v
}

// inWorkspace reports whether dir lies inside a repo of the workspace.
func (v *view) inWorkspace(dir string) bool {
	for _, root := range v.roots {
		if dir == root || strings.HasPrefix(dir, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// isProvided reports whether an indexed repo of the workspace provides
// the identity name of a language family (spec 7.3).
func (v *view) isProvided(lang, name string) bool {
	key := [2]string{lang, name}
	if got, ok := v.provided[key]; ok {
		return got
	}
	repos, err := v.s.ReposProviding(lang, name)
	v.provided[key] = err == nil && len(repos) > 0
	return v.provided[key]
}

// manifestsOf returns the manifests among the indexed files of a repo,
// by path, each with its ecosystem. Indexed files leave excluded
// directories out, node_modules among them (spec 7.1).
func manifestsOf(s *store.Store, repoID int64) (map[string]Ecosystem, error) {
	out := map[string]Ecosystem{}
	for _, e := range Ecosystems() {
		paths, err := s.FilesNamed(repoID, e.Manifest())
		if err != nil {
			return nil, err
		}
		for _, p := range paths {
			out[p] = e
		}
	}
	return out, nil
}

// trackedPaths lists the repo-relative files whose change calls for
// discovery again: each manifest, and the lock files and install markers
// of its ecosystem next to it and at the repo root.
func trackedPaths(manifests map[string]Ecosystem) []string {
	set := map[string]bool{}
	for p, e := range manifests {
		set[p] = true
		for _, t := range e.Tracked() {
			set[path.Join(path.Dir(p), t)] = true
			set[t] = true
		}
	}
	return sortedKeys(set)
}

// snapshot stats the tracked files of a repo that exist and hashes those
// whose size or mtime differs from the stored row; changed reports
// whether the set of files or the content of one differs from stored.
func snapshot(root string, tracked []string, stored []store.DepManifest) (now []store.DepManifest, changed bool) {
	old := make(map[string]store.DepManifest, len(stored))
	for _, m := range stored {
		old[m.Path] = m
	}
	for _, rel := range tracked {
		full := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Stat(full)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		m := store.DepManifest{Path: rel, Size: info.Size(), Mtime: info.ModTime().UnixNano()}
		prev, known := old[rel]
		if known && prev.Size == m.Size && prev.Mtime == m.Mtime {
			m.Hash = prev.Hash
		} else if data, err := os.ReadFile(full); err == nil {
			m.Hash = index.ContentHash(data)
		}
		changed = changed || !known || prev.Hash != m.Hash
		now = append(now, m)
	}
	return now, changed || len(now) != len(stored)
}

// appeared reports whether the directory of a use recorded as missing
// exists now: a Go module downloaded since.
func appeared(uses []store.DepUse) bool {
	for _, u := range uses {
		if u.State != store.DepMissing || u.Dir == "" {
			continue
		}
		if info, err := os.Stat(u.Dir); err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

// discover runs every manifest of a repo through its ecosystem and
// returns the uses, ordered by manifest, ecosystem and name, without the
// names deps.exclude matches. A manifest that cannot be read yields no
// uses and a line in problems. A package found only because
// deps.include names it (a plain name, not a pattern) is recorded once,
// on the first manifest that resolves it.
func (v *view) discover(root string, manifests map[string]Ecosystem) (uses []store.DepUse, problems []string) {
	seen := map[[3]string]bool{}
	for _, p := range sortedKeys(manifests) {
		got, err := manifests[p].Discover(Manifest{Root: root, Path: p, Include: v.ws.Deps.IncludeNames(),
			InWorkspace: v.inWorkspace, Provided: v.isProvided})
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", p, err))
			continue
		}
		for _, u := range got {
			key := [3]string{u.Manifest, u.Eco, u.Name}
			if u.Declared == "" {
				key[0] = ""
			}
			if !v.ws.Deps.Excluded(u.Name) && !seen[key] {
				seen[key] = true
				uses = append(uses, u)
			}
		}
	}
	sort.SliceStable(uses, func(i, j int) bool {
		a, b := uses[i], uses[j]
		if a.Manifest != b.Manifest {
			return a.Manifest < b.Manifest
		}
		if a.Eco != b.Eco {
			return a.Eco < b.Eco
		}
		return a.Name < b.Name
	})
	return uses, problems
}

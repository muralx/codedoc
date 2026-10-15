package query

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/muralx/codedoc/internal/deps"
	"github.com/muralx/codedoc/internal/store"
)

// depView is what a query sees of the shared dependency database: the
// package versions the manifests of the workspace, or of the --repo
// repo, resolve to, and nothing of any other workspace (deps spec
// section 2).
type depView struct {
	db     *deps.DB
	s      *store.Store
	pkgs   []*depPkg         // by package id
	byRepo map[int64]*depPkg // deps.db repo row
}

// depPkg is one package version of the view.
type depPkg struct {
	id     deps.ID
	repoID int64
	// roots are the directories the package is installed in, those of
	// the current repo first: what its file rows are relative to.
	roots []string
}

// tag marks an entry of the package: dep name@version.
func (p *depPkg) tag() string { return "dep " + p.id.Name + "@" + p.id.Version }

// root returns the first directory the package is still installed in,
// "" when none is left.
func (p *depPkg) root() string {
	for _, dir := range p.roots {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	return ""
}

// depSym is a symbol of a dependency. via names the type it was asked
// through when it is inherited: Server for Server.notification, which
// Protocol declares.
type depSym struct {
	store.Symbol
	pkg *depPkg
	via string
}

// Close releases what the context opened: the dependency database.
func (c *Ctx) Close() error {
	if c.view == nil {
		return nil
	}
	v := c.view
	c.view = nil
	return v.db.Close()
}

// deps returns the dependency view, opened on first use, and marks the
// answer as one that used dependencies. It is nil when the workspace has
// no indexed package; a deps.db that cannot be read is a note on the
// answer, never an error of the query (deps spec section 9).
func (c *Ctx) deps() *depView {
	c.usedDeps = true
	if c.viewTried {
		return c.view
	}
	c.viewTried = true
	v, err := c.openView()
	if err != nil {
		c.notes = append(c.notes, "deps: "+err.Error())
	}
	c.view = v
	return v
}

// openView reads the package versions the query may see.
func (c *Ctx) openView() (*depView, error) {
	if _, err := os.Stat(c.DepsDB); c.DepsDB == "" || errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	repoID, err := c.filterID()
	if err != nil {
		return nil, err
	}
	ids, err := deps.PackageIDs(c.Store, repoID)
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	uses, err := c.Store.DepUses(max(repoID, 0))
	if err != nil {
		return nil, err
	}
	db, err := deps.Open(c.DepsDB)
	if err != nil {
		return nil, err
	}
	rows, err := db.RepoIDs(ids)
	if err != nil {
		return nil, errors.Join(err, db.Close())
	}
	v := &depView{db: db, s: db.Store(), byRepo: map[int64]*depPkg{}}
	for _, raw := range ids {
		id, ok := deps.ParseID(raw)
		if rows[raw] == 0 || !ok {
			continue
		}
		p := &depPkg{id: id, repoID: rows[raw], roots: c.rootsOf(raw, uses)}
		v.pkgs, v.byRepo[p.repoID] = append(v.pkgs, p), p
	}
	return v, nil
}

// rootsOf returns the distinct directories the uses install a package
// version in, those of the current repo first.
func (c *Ctx) rootsOf(id string, uses []store.DepUse) []string {
	current := int64(0)
	if c.Repo != nil {
		current, _ = c.repoID(c.Repo)
	}
	var mine, others []string
	seen := map[string]bool{}
	for _, u := range uses {
		root := u.Root()
		if u.PackageID() != id || root == "" || seen[root] {
			continue
		}
		seen[root] = true
		if u.RepoID == current {
			mine = append(mine, root)
		} else {
			others = append(others, root)
		}
	}
	return append(mine, others...)
}

// repoIDs returns the deps.db repo rows of the packages of the view.
func (v *depView) repoIDs() []int64 {
	out := make([]int64, len(v.pkgs))
	for i, p := range v.pkgs {
		out[i] = p.repoID
	}
	return out
}

// lookup is the name lookup over the packages of the view.
func (v *depView) lookup() lookup {
	return lookup{s: v.s, repos: v.repoIDs(), keep: func(sy store.Symbol) bool { return v.byRepo[sy.RepoID] != nil }}
}

// wrap pairs symbols of deps.db with their packages.
func (v *depView) wrap(syms []store.Symbol) []depSym {
	out := make([]depSym, 0, len(syms))
	for _, sy := range syms {
		if p := v.byRepo[sy.RepoID]; p != nil {
			out = append(out, depSym{Symbol: sy, pkg: p})
		}
	}
	return out
}

// match finds the dependency symbols a name, qualified name or glob
// names. A qualified name that matches nothing directly is tried as an
// inherited member: Server.notification finds Protocol.notification when
// Server extends Protocol. A nil view has none.
func (v *depView) match(pattern, kind string) ([]depSym, error) {
	if v == nil {
		return nil, nil
	}
	syms, err := v.lookup().match(pattern, kind)
	if err != nil || len(syms) > 0 || strings.ContainsAny(pattern, "*?[") {
		return v.wrap(syms), err
	}
	return v.inherited(pattern, kind)
}

// byKey returns the dependency symbol with a stable key; ok is false
// when the view has no such symbol.
func (v *depView) byKey(key string) (depSym, bool, error) {
	if v == nil {
		return depSym{}, false, nil
	}
	sy, err := v.s.SymbolByKey(key)
	if errors.Is(err, store.ErrNotFound) || (err == nil && v.byRepo[sy.RepoID] == nil) {
		return depSym{}, false, nil
	}
	return depSym{Symbol: sy, pkg: v.byRepo[sy.RepoID]}, err == nil, err
}

// byKeys returns the dependency symbols with the given stable keys, by
// key; unknown keys are absent.
func (v *depView) byKeys(keys []string) (map[string]depSym, error) {
	out := map[string]depSym{}
	for _, k := range keys {
		d, ok, err := v.byKey(k)
		if err != nil {
			return nil, err
		}
		if ok {
			out[k] = d
		}
	}
	return out, nil
}

// named returns the packages a package id, name@version or name names,
// in id order: two when the workspace uses two versions of one name.
func (v *depView) named(name string) []*depPkg {
	if v == nil {
		return nil
	}
	var out []*depPkg
	for _, p := range v.pkgs {
		if name == p.id.String() || name == p.id.Name || name == p.id.Name+"@"+p.id.Version {
			out = append(out, p)
		}
	}
	return out
}

// located finds the package an absolute path lies in and the path
// relative to the package, by the directories the package is installed
// in; symbolic links (pnpm) are followed on both sides.
func (v *depView) located(abs string) (*depPkg, string, bool) {
	if v == nil {
		return nil, "", false
	}
	candidates := []string{filepath.Clean(abs)}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil && resolved != candidates[0] {
		candidates = append(candidates, resolved)
	}
	var best *depPkg
	rel, depth := "", -1
	for _, p := range v.pkgs {
		for _, root := range p.roots {
			for _, cand := range candidates {
				if r, ok := relWithin(root, cand); ok && len(root) > depth {
					best, rel, depth = p, r, len(root)
				}
			}
		}
	}
	return best, rel, best != nil
}

// sortDeps orders dependency symbols by package, path and line.
func sortDeps(syms []depSym) {
	sort.SliceStable(syms, func(i, j int) bool {
		a, b := syms[i], syms[j]
		if a.pkg.id != b.pkg.id {
			return a.pkg.id.String() < b.pkg.id.String()
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.StartLine < b.StartLine
	})
}

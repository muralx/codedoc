package resolve

import (
	"errors"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/store"
)

// ecosystems maps a language family to the package ecosystem its
// imports name (deps spec section 3).
var ecosystems = map[string]string{"go": "go", "ts": "npm"}

// Ecosystem returns the package ecosystem the imports of a language
// family name, "" for a family without one.
func Ecosystem(family string) string { return ecosystems[family] }

// depSource finds the symbols of third-party packages that references
// of workspace code land in (deps spec section 6). It reads the indexed
// dependency uses of the workspace once and deps.db only when an import
// names one of those packages, so a workspace without dependencies pays
// one small query per run.
type depSource struct {
	b    *store.Batch
	path string
	db   *store.Store
	// loaded is set once the uses were read; off when there is nothing
	// to resolve against.
	loaded, off bool
	uses        map[string][]store.DepUse // eco, name
	pkgs        map[string]*depPkg        // package id; nil when deps.db lacks it
	binds       map[string]depBind        // repo, directory, specifier
}

// depPkg is one package version of deps.db.
type depPkg struct {
	repoID  int64
	modules map[string]int64 // import specifier: module row
	named   map[string][]store.Symbol
	// exports is the re-export graph of an npm package, nil when the
	// package has none.
	exports *DepExports
}

// depBind is the package an import names, with the module its specifier
// names (0 when the package exports no such entry point). spec is the
// specifier as the package itself names it: under an npm alias
// ("lodash": "npm:lodash-es@4") lodash/fp is lodash-es/fp.
type depBind struct {
	pkg    *depPkg
	module int64
	spec   string
}

// newDepSource returns the source of the workspace b writes to.
func newDepSource(b *store.Batch) *depSource {
	return &depSource{b: b, uses: map[string][]store.DepUse{}, pkgs: map[string]*depPkg{},
		binds: map[string]depBind{}}
}

// close releases deps.db.
func (d *depSource) close() error {
	if d.db == nil {
		return nil
	}
	return d.db.Close()
}

// ready reports whether there are packages to resolve against: the
// workspace uses indexed packages and deps.db opens. The first call
// reads the uses and where deps.db is. A resolver without a source
// (nil) never is.
func (d *depSource) ready() bool {
	if d == nil || d.off {
		return false
	}
	if d.loaded {
		return true
	}
	d.loaded = true
	uses, err := d.b.IndexedDepUses()
	if err != nil || len(uses) == 0 || !d.locate() {
		d.off = true
		return false
	}
	for _, u := range uses {
		d.uses[u.Eco+"\x00"+u.Name] = append(d.uses[u.Eco+"\x00"+u.Name], u)
	}
	if d.db, err = store.Open(d.path); err != nil {
		d.off = true
	}
	return !d.off
}

// locate reads the path of deps.db that the last dependency sync
// recorded in the workspace database; false when there is none or the
// file is gone.
func (d *depSource) locate() bool {
	path, ok, err := d.b.Meta(store.DepsDBKey)
	if err != nil || !ok {
		return false
	}
	d.path = path
	_, err = os.Stat(path)
	return err == nil
}

// bind returns the package version an import of a file resolves to: the
// one the nearest manifest declaring the package resolves it to, walking
// up from the file's directory as Node and the go command do.
func (d *depSource) bind(imp store.Import, fam string) (depBind, bool) {
	key := strconv.FormatInt(imp.RepoID, 10) + "\x00" + path.Dir(imp.Path) + "\x00" + imp.Raw
	if bd, ok := d.binds[key]; ok {
		return bd, bd.pkg != nil
	}
	bd := depBind{spec: imp.Raw}
	if u, ok := d.use(imp, ecosystems[fam]); ok {
		if u.Package != "" {
			bd.spec = u.Package + strings.TrimPrefix(imp.Raw, u.Name)
		}
		if bd.pkg = d.pkg(u.PackageID()); bd.pkg != nil {
			bd.module = bd.pkg.modules[bd.spec]
		}
	}
	d.binds[key] = bd
	return bd, bd.pkg != nil
}

// use returns the dependency use an import specifier names for its file.
func (d *depSource) use(imp store.Import, eco string) (store.DepUse, bool) {
	var best store.DepUse
	found := false
	dir := path.Dir(imp.Path)
	for _, name := range packageNames(eco, imp.Raw) {
		for _, u := range d.uses[eco+"\x00"+name] {
			at := path.Dir(u.Manifest)
			inside := at == "." || dir == at || strings.HasPrefix(dir, at+"/")
			if u.RepoID == imp.RepoID && inside && (!found || len(at) > len(path.Dir(best.Manifest))) {
				best, found = u, true
			}
		}
		if found {
			return best, true
		}
	}
	return best, false
}

// packageNames returns the package names an import specifier may belong
// to, most specific first: for npm the package of a bare specifier, for
// Go every path prefix (the module path is not marked in an import).
func packageNames(eco, raw string) []string {
	switch {
	case eco == "npm" && !isRelative(raw) && !strings.Contains(raw, ":"):
		pkg, _ := splitPackage(raw)
		return []string{pkg}
	case eco != "go":
		return nil
	}
	var out []string
	for p := raw; p != "." && p != "/"; p = path.Dir(p) {
		out = append(out, p)
	}
	return out
}

// pkg returns the rows of a package version, nil when deps.db does not
// hold it.
func (d *depSource) pkg(id string) *depPkg {
	if p, ok := d.pkgs[id]; ok {
		return p
	}
	d.pkgs[id] = nil
	row, err := d.db.RepoByName(config.DepsWorkspace, id)
	if err != nil {
		return nil
	}
	ids, err := d.db.RepoIdentities(row.ID)
	mods, merr := d.db.ModulesByRepo(row.ID)
	if errors.Join(err, merr) != nil {
		return nil
	}
	byPath := make(map[string]int64, len(mods))
	for _, m := range mods {
		byPath[m.Path] = m.ID
	}
	p := &depPkg{repoID: row.ID, modules: make(map[string]int64, len(ids)), named: map[string][]store.Symbol{}}
	for _, id := range ids {
		p.modules[id.Identity] = byPath[id.Path]
	}
	if raw, ok, err := d.db.Meta(DepExportsKey + id); err == nil && ok {
		p.exports, _ = DecodeDepExports(raw)
	}
	d.pkgs[id] = p
	return p
}

// exported returns the top-level declaration the bound specifier of the
// package exports under a name through its re-exports, or inside the namespace
// ns it exports (see DepExports.Lookup): the first one in the nearest
// file that declares it.
func (d *depSource) exported(bd depBind, ns, name string, kinds []string) (store.Symbol, bool) {
	files, name := bd.pkg.exports.Lookup(bd.spec, ns, name)
	if len(files) == 0 {
		return store.Symbol{}, false
	}
	syms := d.symbols(bd.pkg, name, kinds)
	for _, file := range files {
		for _, sy := range syms {
			if sy.ContainerID == 0 && sy.Path == file {
				return sy, true
			}
		}
	}
	return store.Symbol{}, false
}

// symbols returns the symbols of a package with a name and one of kinds
// (nil for any), in path and line order.
func (d *depSource) symbols(p *depPkg, name string, kinds []string) []store.Symbol {
	syms, ok := p.named[name]
	if !ok {
		syms, _ = d.db.SymbolsByName(name, "", p.repoID)
		p.named[name] = syms
	}
	if kinds == nil {
		return syms
	}
	var out []store.Symbol
	for _, sy := range syms {
		if slices.Contains(kinds, sy.Kind) {
			out = append(out, sy)
		}
	}
	return out
}

// top returns the top-level declaration an import brings in under a
// name: the first one of the module the specifier names, else, unless
// exact, the only one of the whole package (a name an entry point
// re-exports from another file).
func (d *depSource) top(bd depBind, name string, kinds []string, exact bool) (store.Symbol, bool) {
	var all []store.Symbol
	for _, sy := range d.symbols(bd.pkg, name, kinds) {
		if sy.ContainerID != 0 {
			continue
		}
		if sy.ModuleID == bd.module {
			return sy, true
		}
		all = append(all, sy)
	}
	if !exact && len(all) == 1 {
		return all[0], true
	}
	return store.Symbol{}, false
}

// member returns the member of a container with a name: its own, else
// the one it inherits through the extends edges of deps.db, nearest
// supertype first. An inherited member is the symbol of the type that
// declares it.
func (d *depSource) member(p *depPkg, container store.Symbol, name string, kinds []string) (store.Symbol, bool) {
	seen := map[int64]bool{}
	queue := []int64{container.ID}
	for len(queue) > 0 && len(seen) < maxInheritance {
		id := queue[0]
		queue = queue[1:]
		if seen[id] {
			continue
		}
		seen[id] = true
		for _, sy := range d.symbols(p, name, kinds) {
			if sy.ContainerID == id {
				return sy, true
			}
		}
		supers, err := d.db.EdgesFrom(id, EdgeExtends)
		if err != nil {
			break
		}
		for _, e := range supers {
			queue = append(queue, e.Dst)
		}
	}
	return store.Symbol{}, false
}

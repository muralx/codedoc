package deps

import (
	"errors"
	"path"
	"strings"

	"github.com/muralx/codedoc/internal/parse"
	"github.com/muralx/codedoc/internal/resolve"
	"github.com/muralx/codedoc/internal/store"
)

// Which packages are indexed (deps spec section 4, "Imported
// packages"): an installed package is indexed when a source file below
// its manifest imports it or deps.include names it; the others are
// recorded as unused. Whether a manifest lists a package under
// dependencies or devDependencies decides nothing.

// imports holds what the source files of one repo import from outside
// it: per ecosystem and specifier, the directories of the importing
// files.
type imports map[string]map[string][]string

// readImports collects the imports of a repo from the workspace index.
// The imports of a tool configuration file do not count (see
// toolConfig).
func readImports(s *store.Store, repoID int64) (imports, error) {
	sites, err := s.ImportSites(repoID)
	if err != nil {
		return nil, err
	}
	im := imports{}
	for _, site := range sites {
		eco := resolve.Ecosystem(parse.Family(site.Lang))
		if eco == "" || toolConfig(site.Path) {
			continue
		}
		if im[eco] == nil {
			im[eco] = map[string][]string{}
		}
		if dir := path.Dir(site.Path); !contains(im[eco][site.Raw], dir) {
			im[eco][site.Raw] = append(im[eco][site.Raw], dir)
		}
	}
	return im, nil
}

// configExts are the extensions a tool configuration file is written
// in.
var configExts = []string{".js", ".cjs", ".mjs", ".ts", ".cts", ".mts"}

// toolConfig reports whether a path names the configuration file of a
// build tool rather than code of the workspace: <tool>.config.<ext>
// (eslint.config.js, vite.config.ts) or a dot file (.eslintrc.cjs).
// Such a file imports the tools it configures, which says nothing about
// what the code uses.
func toolConfig(rel string) bool {
	base := path.Base(rel)
	for _, ext := range configExts {
		if stem, ok := strings.CutSuffix(base, ext); ok {
			return strings.HasPrefix(base, ".") || strings.HasSuffix(stem, ".config")
		}
	}
	return false
}

// inside reports whether the directory dir is the directory at of a
// manifest or lies below it.
func inside(dir, at string) bool {
	return at == "." || dir == at || strings.HasPrefix(dir, at+"/")
}

// imported reports whether a file below the manifest directory at
// imports the package name of an ecosystem: the specifier is the name
// or continues it with a slash (a subpath, a Go package of the module).
func (im imports) imported(eco, at, name string) bool {
	for spec, dirs := range im[eco] {
		if spec != name && !strings.HasPrefix(spec, name+"/") {
			continue
		}
		if anyInside(dirs, at) {
			return true
		}
	}
	return false
}

// anyInside reports whether one of dirs is inside at.
func anyInside(dirs []string, at string) bool {
	for _, dir := range dirs {
		if inside(dir, at) {
			return true
		}
	}
	return false
}

// typed returns the package an @types package declares: plain-js for
// @types/plain-js, @scope/name for @types/scope__name; "" for any other
// name.
func typed(name string) string {
	rest, ok := strings.CutPrefix(name, "@types/")
	if !ok {
		return ""
	}
	if scope, pkg, scoped := strings.Cut(rest, "__"); scoped {
		return "@" + scope + "/" + pkg
	}
	return rest
}

// usage decides which uses of one repo the workspace wants indexed.
type usage struct {
	v  *view
	im imports
	// declared holds the npm names the manifests of the repo declare.
	declared map[string]bool
}

// wanted reports whether a use is indexed: deps.include names it or
// found it (a transitive package has no declared range), or a file
// below its manifest imports it. An @types package follows the package
// it declares, and @types/node the built-in modules imported, which are
// returned: only their declaration files are indexed (see ambient).
func (g *usage) wanted(u store.DepUse) (want bool, only []string) {
	at := path.Dir(u.Manifest)
	switch {
	case u.Declared == "" || g.v.ws.Deps.Included(u.Name):
		return true, nil
	case ambient(u):
		only = g.im.nodeModules(u.Root(), at, g.declared)
		return len(only) > 0, only
	case g.im.imported(u.Eco, at, u.Name):
		return true, nil
	}
	of := typed(u.Name)
	return u.Eco == "npm" && of != "" && g.im.imported(u.Eco, at, of), nil
}

// mark gives every installed use of a repo the state its imports call
// for: unused when nothing wants it, else the state it had, a use that
// was unused becoming pending or what its package version already is in
// the workspace. It records in v.only the modules wanted of each
// partially indexed package.
func (v *view) mark(repoID int64, uses []store.DepUse) error {
	im, err := v.importsOf(repoID)
	if err != nil {
		return err
	}
	known, err := v.knownStates()
	if err != nil {
		return err
	}
	g := &usage{v: v, im: im, declared: map[string]bool{}}
	for _, u := range uses {
		g.declared[u.Name] = u.Eco == "npm" || g.declared[u.Name]
	}
	for i := range uses {
		if u := &uses[i]; u.Version != "" && u.State != store.DepSkipped && u.State != store.DepMissing {
			g.settle(u, known)
		}
	}
	return nil
}

// settle gives one installed use its state (see mark); known holds an
// indexed or failed use of the workspace by package id.
func (g *usage) settle(u *store.DepUse, known map[string]store.DepUse) {
	want, only := g.wanted(*u)
	if !want {
		u.State, u.Error = store.DepUnused, ""
		return
	}
	g.v.only.add(u.PackageID(), only)
	if u.State != store.DepUnused {
		return
	}
	u.State = store.DepPending
	if prev, ok := known[u.PackageID()]; ok {
		u.State, u.Error = prev.State, prev.Error
	}
}

// importsOf returns the imports of a repo, read once per view.
func (v *view) importsOf(repoID int64) (imports, error) {
	if im, ok := v.imports[repoID]; ok {
		return im, nil
	}
	im, err := readImports(v.s, repoID)
	if err == nil {
		v.imports[repoID] = im
	}
	return im, err
}

// applyUsage marks the uses of the repos ids (see mark) and stores the
// states that changed, under the workspace write lock: the code started
// or stopped importing a package since discovery last ran.
func (v *view) applyUsage(ids map[string]int64) error {
	var changed []store.DepUse
	for _, name := range sortedKeys(ids) {
		uses, err := v.s.DepUses(ids[name])
		if err != nil {
			return err
		}
		states := make([]string, len(uses))
		for i, u := range uses {
			states[i] = u.State
		}
		if err := v.mark(ids[name], uses); err != nil {
			return err
		}
		for i, u := range uses {
			if u.State != states[i] {
				changed = append(changed, u)
			}
		}
	}
	if len(changed) == 0 {
		return nil
	}
	return setStates(v.s, changed)
}

// setStates stores the state of each use under the workspace write
// lock.
func setStates(s *store.Store, uses []store.DepUse) (err error) {
	unlock, err := s.WriteLock()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	return s.Batch(func(b *store.Batch) error {
		for _, u := range uses {
			if err := b.SetDepUseState(u); err != nil {
				return err
			}
		}
		return nil
	})
}

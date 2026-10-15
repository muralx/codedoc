package deps

import (
	"encoding/json"
	"os"
	"sort"
	"strings"

	"github.com/muralx/codedoc/internal/store"
)

// An ambient package declares modules that no specifier of its own
// names. The one codedoc knows is @types/node: `import "node:fs"` reads
// the declarations of fs.d.ts of that package. Such a package is
// indexed lazily (deps spec section 5, "Ambient packages"): only the
// declaration files of the modules the workspace imports, and the files
// those reach through relative imports and reference directives. What
// they import by module name (node:stream from fs.d.ts) is not
// followed, so a supertype declared in a module the workspace does not
// import has no rows.

// nodeTypes is the package declaring the built-in modules of Node.js.
const nodeTypes = "@types/node"

// ambient reports whether a use is of an ambient package.
func ambient(u store.DepUse) bool { return u.Eco == "npm" && u.Name == nodeTypes }

// ambientID reports whether a package id is that of an ambient package.
func ambientID(id string) bool {
	parsed, ok := ParseID(id)
	return ok && parsed.Eco == "npm" && parsed.Name == nodeTypes
}

// nodeScheme starts the specifier of a built-in module: node:fs.
const nodeScheme = "node:"

// modulesKey prefixes the meta key listing, as a JSON array, the
// modules a partially extracted package version holds. An ambient
// package extracted whole because a workspace asked for it holds
// wholePackage there. No key means a whole package too: any package
// that is not ambient, and an ambient one written before ambient
// packages were indexed lazily, which is extracted again (see covers).
const (
	modulesKey   = "dep_modules:"
	wholePackage = "*"
)

// nodeModules returns the built-in modules the files below the manifest
// directory at import, named as the declaration files of the package at
// root are (fs, fs/promises), sorted. A specifier without the node:
// scheme is one only when the manifests declare no package of that
// name.
func (im imports) nodeModules(root, at string, declared map[string]bool) []string {
	var out []string
	for spec, dirs := range im["npm"] {
		mod, schemed := strings.CutPrefix(spec, nodeScheme)
		if first, _, _ := strings.Cut(mod, "/"); !schemed && (declared[first] || strings.HasPrefix(mod, "@")) {
			continue
		}
		if !contains(out, mod) && anyInside(dirs, at) && ambientFile(root, mod) != "" {
			out = append(out, mod)
		}
	}
	sort.Strings(out)
	return out
}

// ambientFile returns the declaration file of a module of an ambient
// package, relative to the package at root: <module>.d.ts. It returns
// "" when the package has no such file or the name leaves the package.
func ambientFile(root, mod string) string {
	rel := mod + ".d.ts"
	full, err := within(root, rel)
	if err != nil {
		return ""
	}
	if info, err := os.Stat(full); err != nil || !info.Mode().IsRegular() {
		return ""
	}
	return rel
}

// wants collects, per package id, the modules wanted of the packages
// indexed partially. A package some use wants whole is whole.
type wants struct {
	only  map[string][]string
	whole map[string]bool
}

// add records that a use wants the modules only of a package, the whole
// package when only is nil.
func (w *wants) add(id string, only []string) {
	if only == nil {
		w.whole[id] = true
		delete(w.only, id)
		return
	}
	if !w.whole[id] {
		w.only[id] = union(w.only[id], only)
	}
}

// union returns the distinct strings of a and b, sorted.
func union(a, b []string) []string {
	out := append([]string{}, a...)
	for _, s := range b {
		if !contains(out, s) {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// held returns the modules a partially extracted package version holds;
// partial is false for a package extracted whole, or not at all.
func (d *DB) held(id string) (mods []string, partial bool) {
	raw, ok, err := d.s.Meta(modulesKey + id)
	if err != nil || !ok || raw == wholePackage || json.Unmarshal([]byte(raw), &mods) != nil {
		return nil, false
	}
	return mods, true
}

// legacy reports whether the rows of an ambient package version were
// written whole by a build that knew no lazy indexing: they carry no
// module key at all.
func (d *DB) legacy(id string) bool {
	_, marked, err := d.s.Meta(modulesKey + id)
	return err == nil && !marked && ambientID(id)
}

// holds reports whether a package version is indexed with the rows this
// build writes (see Current) and they hold the modules only, or the
// whole package when only is nil (see covers).
func (d *DB) holds(id string, only []string) (bool, error) {
	current, err := d.Current(id)
	return current && d.covers(id, only), err
}

// covers reports whether the rows of an indexed package version hold
// the modules only, or the whole package when only is nil. The whole
// rows an older build wrote of an ambient package cover nothing: they
// are replaced by what the workspace wants (see legacy).
func (d *DB) covers(id string, only []string) bool {
	have, partial := d.held(id)
	if !partial {
		return !d.legacy(id)
	}
	for _, mod := range only {
		if !contains(have, mod) {
			return false
		}
	}
	return only != nil
}

// widen returns what to extract of a package version so that its rows
// hold the modules only besides those they hold already: a partial
// package only grows, so two workspaces sharing it settle on the union
// of what they import. nil, the whole package, stays nil.
func (d *DB) widen(id string, only []string) []string {
	if only == nil {
		return nil
	}
	have, _ := d.held(id)
	return union(have, only)
}

// encodeModules renders what an extraction of the package id read for
// the meta key: the modules of a partial one, wholePackage for a whole
// ambient package, "" (no key) for any other package.
func encodeModules(id string, only []string) string {
	switch {
	case only != nil:
		raw, _ := json.Marshal(only)
		return string(raw)
	case ambientID(id):
		return wholePackage
	}
	return ""
}

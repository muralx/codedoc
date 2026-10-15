package deps

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/muralx/codedoc/internal/store"
)

func init() { register(npm{}) }

// npm is the ecosystem of package.json manifests and node_modules, in
// the npm, pnpm and yarn layouts.
type npm struct{}

// Name is the id segment of npm packages.
func (npm) Name() string { return "npm" }

// Manifest is package.json.
func (npm) Manifest() string { return "package.json" }

// Tracked lists the lock files of npm, pnpm and yarn and the marker each
// writes into node_modules when it installs.
func (npm) Tracked() []string {
	return []string{"package-lock.json", "npm-shrinkwrap.json", "pnpm-lock.yaml", "yarn.lock",
		"node_modules/.package-lock.json", "node_modules/.modules.yaml", "node_modules/.yarn-integrity"}
}

// packageJSON is what discovery and extraction read of a package.json.
type packageJSON struct {
	Name    string          `json:"name"`
	Version string          `json:"version"`
	Types   string          `json:"types"`
	Typings string          `json:"typings"`
	Main    string          `json:"main"`
	Exports json.RawMessage `json:"exports"`

	Dependencies         map[string]string `json:"dependencies"`
	DevDependencies      map[string]string `json:"devDependencies"`
	PeerDependencies     map[string]string `json:"peerDependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
}

// readPackageJSON reads dir/package.json, which must be a file of dir
// itself (see within) of at most maxManifest bytes.
func readPackageJSON(dir string) (packageJSON, error) {
	var pj packageJSON
	full, err := within(dir, "package.json")
	if err != nil {
		return pj, err
	}
	return pj, readJSON(full, &pj)
}

// declared merges the four dependency maps of a manifest; a name in
// several keeps the range of the first (dependencies, dev, peer,
// optional).
func (pj packageJSON) declared() map[string]string {
	out := map[string]string{}
	for _, m := range []map[string]string{pj.Dependencies, pj.DevDependencies, pj.PeerDependencies,
		pj.OptionalDependencies} {
		for name, rng := range m {
			if _, ok := out[name]; !ok {
				out[name] = rng
			}
		}
	}
	return out
}

// devOnly reports whether a manifest declares a package under
// devDependencies and nowhere else.
func (pj packageJSON) devOnly(name string) bool {
	_, dev := pj.DevDependencies[name]
	for _, m := range []map[string]string{pj.Dependencies, pj.PeerDependencies, pj.OptionalDependencies} {
		if _, ok := m[name]; ok {
			return false
		}
	}
	return dev
}

// Discover resolves the packages a package.json declares the way Node
// does (see installed). An included name is resolved the same way, and
// also in pnpm's hoisting directory; one that is not found yields no
// use. An @types package that only supplies the declarations of another
// declared package is not a use of its own.
func (e npm) Discover(m Manifest) ([]store.DepUse, error) {
	pj, err := readPackageJSON(m.Dir())
	if err != nil {
		return nil, err
	}
	declared := pj.declared()
	var uses []store.DepUse
	supplied := map[string]bool{}
	patches := patchesOf(m)
	for _, name := range sortedKeys(declared) {
		u := e.resolve(m, name, declared[name], false, patches)
		u.Dev = pj.devOnly(name)
		if u.TypesDir != "" {
			supplied[u.TypesDir] = true
		}
		uses = append(uses, u)
	}
	for _, name := range m.Include {
		if _, ok := declared[name]; ok {
			continue
		}
		if u := e.resolve(m, name, "", true, patches); u.State != store.DepMissing {
			uses = append(uses, u)
		}
	}
	kept := uses[:0]
	for _, u := range uses {
		if !strings.HasPrefix(u.Name, "@types/") || !supplied[u.Dir] {
			kept = append(kept, u)
		}
	}
	return kept, nil
}

// resolve finds one declared package. A package installed inside a
// workspace repo and outside any node_modules, or not installed but
// provided by a workspace repo, is a workspace package and skipped. An
// installed package without declarations of its own takes them from
// @types/<name> when that is installed. The package is identified by
// the name of the installed package.json, which differs from the
// declared one under an alias ("lodash": "npm:lodash-es@4"); a mutable
// source (see npmMutable) gets a local version.
func (e npm) resolve(m Manifest, name, rng string, hoisted bool, patches npmPatches) store.DepUse {
	u := store.DepUse{Manifest: m.Path, Eco: e.Name(), Name: name, Declared: rng, State: store.DepMissing}
	dir, ok := installed(m, name, hoisted)
	if !ok {
		if m.Provided != nil && m.Provided("ts", name) {
			u.State = store.DepSkipped
		}
		return u
	}
	u.Dir = dir
	if m.InWorkspace != nil && m.InWorkspace(dir) && !inNodeModules(dir) {
		u.State = store.DepSkipped
		return u
	}
	pj, err := readPackageJSON(dir)
	if err != nil || pj.Version == "" {
		return u
	}
	u.Version, u.State = pj.Version, store.DepPending
	if pj.Name != "" && pj.Name != name {
		u.Package = pj.Name
	}
	if npmMutable(rng, dir, u.PackageName(), pj.Version, patches) {
		u.Version = localVersion(dir)
	}
	u.TypesDir = typesDir(m, u.PackageName(), dir, pj, hoisted)
	return u
}

// typesDir returns the directory of the @types package that supplies
// the declarations of an installed package shipping none, "" when it
// has its own or none is installed.
func typesDir(m Manifest, name, dir string, pj packageJSON, hoisted bool) string {
	if len(npmEntries(dir, pj)) > 0 || strings.HasPrefix(name, "@types/") {
		return ""
	}
	types, _ := installed(m, typesName(name), hoisted)
	return types
}

// installed returns the real directory Node resolves a package name to
// from the manifest's directory: node_modules/<name> there or in a
// parent up to the repo root, symbolic links followed (pnpm). With
// hoisted it also looks in node_modules/.pnpm/node_modules, where pnpm
// links every transitive package.
func installed(m Manifest, name string, hoisted bool) (string, bool) {
	for dir := m.Dir(); ; dir = filepath.Dir(dir) {
		cands := []string{filepath.Join(dir, "node_modules", filepath.FromSlash(name))}
		if hoisted {
			cands = append(cands, filepath.Join(dir, "node_modules", ".pnpm", "node_modules", filepath.FromSlash(name)))
		}
		for _, c := range cands {
			if info, err := os.Stat(filepath.Join(c, "package.json")); err == nil && info.Mode().IsRegular() {
				if resolved, err := filepath.EvalSymlinks(c); err == nil {
					return resolved, true
				}
			}
		}
		if dir == m.Root || dir == filepath.Dir(dir) {
			return "", false
		}
	}
}

// inNodeModules reports whether a directory lies below a node_modules
// directory.
func inNodeModules(dir string) bool {
	for _, part := range strings.Split(filepath.ToSlash(dir), "/") {
		if part == "node_modules" {
			return true
		}
	}
	return false
}

// typesName returns the DefinitelyTyped package of a package name:
// @types/name, and @types/scope__name for @scope/name.
func typesName(name string) string {
	if scoped, ok := strings.CutPrefix(name, "@"); ok {
		return "@types/" + strings.Replace(scoped, "/", "__", 1)
	}
	return "@types/" + name
}

// sortedKeys returns the keys of m, sorted.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Package resolve maps repos to the module identities they provide and
// resolves imports and references against the store.
package resolve

import (
	"path"
	"sort"
	"strings"

	"github.com/muralx/codedoc/internal/parse"
)

// Identity is one identity a repo provides, read from a manifest.
type Identity struct {
	Lang     string // language family: go, ts, py or java
	Identity string // Go module path, npm package name, Python distribution name, Maven group/artifact or Gradle root project
	Path     string // manifest path relative to the repo root
}

// Module is one module unit of a repo (spec 7.3, Task 16 rules).
type Module struct {
	Path     string // repo-relative directory, "." for the repo root; a Python file module's file
	Lang     string // language family
	Kind     string // go-package, npm-package, npm-dir, dir, py-package, py-module or java-package
	Identity string // what imports name it by
	Parent   string // Path of the enclosing module, "" when none
	Root     string // package root directory; TS qualified names are relative to it
	// Package is the module segment of the stable keys of the module's
	// symbols (spec 5.1): the identity for Go packages and plain
	// directories, the package name for every module of an npm package.
	Package string
}

// RelPath returns filePath relative to the module's package root, the
// prefix of TypeScript qualified names (spec 5.1).
func (m Module) RelPath(filePath string) string {
	if m.Root == "." {
		return filePath
	}
	return strings.TrimPrefix(filePath, m.Root+"/")
}

// Map is the identity map of one repo.
type Map struct {
	Repo    []Identity // one per distinct identity, from its shallowest manifest, by manifest path
	Modules []Module   // ordered by language, then path
	byKey   map[string]int
}

// ModuleFor returns the module that holds a file of a language family.
func (m *Map) ModuleFor(lang, filePath string) (Module, bool) {
	i, ok := m.byKey[lang+"\x00"+ModulePath(lang, filePath)]
	if !ok {
		return Module{}, false
	}
	return m.Modules[i], true
}

// Identities builds the identity map of the repo at root from its
// discovered files (repo-relative slash paths): Go packages identified
// through the nearest go.mod, TypeScript and JavaScript directories
// through the nearest named package.json, and plain directories, named by
// their repo-relative path, where no manifest applies; Python modules and
// packages by dotted path (see pyModules) and Java packages (see
// javaModule). Manifests that disappeared since discovery, or do not
// parse, are ignored.
func Identities(root string, files []string) (*Map, error) {
	mf, err := readManifests(root, files)
	if err != nil {
		return nil, err
	}
	b := builder{m: &Map{Repo: mf.identities, byKey: map[string]int{}}, mf: mf}
	for _, dir := range sourceDirs(files, "go") {
		b.goModule(dir)
	}
	for _, dir := range sourceDirs(files, "ts") {
		b.tsModule(dir)
	}
	b.pyModules(files)
	declared := javaPackages(root, files)
	for _, dir := range sourceDirs(files, "java") {
		b.javaModule(dir, declared[dir])
	}
	sort.Slice(b.m.Modules, func(i, j int) bool {
		a, c := b.m.Modules[i], b.m.Modules[j]
		return a.Lang < c.Lang || (a.Lang == c.Lang && a.Path < c.Path)
	})
	for i, mod := range b.m.Modules {
		b.m.byKey[mod.Lang+"\x00"+mod.Path] = i
	}
	return b.m, nil
}

// ModulePath returns the path of the module row holding a file of a
// language family: the file itself for a Python module other than a
// package's __init__.py, else the file's directory.
func ModulePath(lang, filePath string) string {
	if lang == "py" && path.Base(filePath) != "__init__.py" {
		return filePath
	}
	return path.Dir(filePath)
}

// sourceDirs returns the sorted directories holding files of a family.
func sourceDirs(files []string, family string) []string {
	seen := map[string]bool{}
	var dirs []string
	for _, f := range files {
		if d := path.Dir(f); parse.Family(parse.LanguageForPath(f)) == family && !seen[d] {
			seen[d] = true
			dirs = append(dirs, d)
		}
	}
	sort.Strings(dirs)
	return dirs
}

// builder accumulates the modules of one repo.
type builder struct {
	m    *Map
	mf   manifests
	seen map[string]bool
}

func (b *builder) add(mod Module) {
	if b.seen == nil {
		b.seen = map[string]bool{}
	}
	if key := mod.Lang + "\x00" + mod.Path; !b.seen[key] {
		b.seen[key] = true
		b.m.Modules = append(b.m.Modules, mod)
	}
}

// goModule adds the package in dir: the go.mod module path joined with
// the directory relative to the go.mod directory.
func (b *builder) goModule(dir string) {
	mod := Module{Path: dir, Lang: "go", Kind: "go-package", Identity: dir, Root: "."}
	if at, ok := nearest(dir, b.mf.goMods); ok {
		mod.Identity = join(b.mf.goMods[at], rel(at, dir))
	}
	mod.Package = mod.Identity
	b.add(mod)
}

// tsModule adds the module of dir and, inside a package, every directory
// between the package root and dir, each linked to its parent directory.
func (b *builder) tsModule(dir string) {
	at, ok := nearest(dir, b.mf.packages)
	if !ok {
		b.add(Module{Path: dir, Lang: "ts", Kind: "dir", Identity: dir, Root: ".", Package: dir})
		return
	}
	name := b.mf.packages[at]
	b.add(Module{Path: at, Lang: "ts", Kind: "npm-package", Identity: name, Root: at, Package: name})
	parent := at
	for _, seg := range splitRel(rel(at, dir)) {
		p := path.Join(parent, seg)
		b.add(Module{Path: p, Lang: "ts", Kind: "npm-dir", Identity: join(name, rel(at, p)), Parent: parent, Root: at,
			Package: name})
		parent = p
	}
}

// nearest returns dir or its closest ancestor that is a key of m.
func nearest(dir string, m map[string]string) (string, bool) {
	for d := dir; ; d = path.Dir(d) {
		if _, ok := m[d]; ok {
			return d, true
		}
		if d == "." || d == "/" {
			return "", false
		}
	}
}

// rel returns dir relative to base, "" when they are equal; both are
// repo-relative and base is dir or one of its ancestors.
func rel(base, dir string) string {
	if base == dir {
		return ""
	}
	if base == "." {
		return dir
	}
	return strings.TrimPrefix(dir, base+"/")
}

func splitRel(r string) []string {
	if r == "" {
		return nil
	}
	return strings.Split(r, "/")
}

func join(identity, r string) string {
	if r == "" {
		return identity
	}
	return identity + "/" + r
}

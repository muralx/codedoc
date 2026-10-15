package resolve

import (
	"path"
	"strings"

	"github.com/muralx/codedoc/internal/parse"
)

// pyModules adds the Python modules of the repo (spec 5.1): every .py file
// is a module named by its dotted path below its package root, nested
// under the module of its directory, the package; a package's __init__.py
// belongs to the package module itself. The package root is the directory
// of the nearest pyproject.toml or setup.py above the file, or its src/
// child when that holds Python files. Files outside every root are named
// by their dotted repo-relative path.
func (b *builder) pyModules(files []string) {
	roots := b.pyRoots(files)
	for _, f := range sortedCopy(files) {
		if parse.Family(parse.LanguageForPath(f)) != "py" {
			continue
		}
		root := pyRootOf(f, roots)
		parent := b.pyPackages(root, path.Dir(f))
		if path.Base(f) == "__init__.py" {
			continue
		}
		id := dotted(rel(root, strings.TrimSuffix(f, ".py")))
		b.add(Module{Path: f, Lang: "py", Kind: "py-module", Identity: id, Parent: parent, Root: ".", Package: id})
	}
}

// pyRoots returns the package roots of the repo: each manifest directory,
// replaced by its src/ child when Python files live there.
func (b *builder) pyRoots(files []string) []string {
	var roots []string
	for dir := range b.mf.pyRoots {
		src := path.Join(dir, "src")
		for _, f := range files {
			if strings.HasPrefix(f, src+"/") && strings.HasSuffix(f, ".py") {
				dir = src
				break
			}
		}
		roots = append(roots, dir)
	}
	return roots
}

// pyRootOf returns the deepest root holding f, or "." when none does.
func pyRootOf(f string, roots []string) string {
	best := "."
	for _, r := range roots {
		if r != "." && strings.HasPrefix(f, r+"/") && (best == "." || len(r) > len(best)) {
			best = r
		}
	}
	return best
}

// pyPackages adds the package modules from root down to dir, each nested
// under the one above, and returns dir's path, or "" when dir is root.
func (b *builder) pyPackages(root, dir string) string {
	at, parent := root, ""
	for _, seg := range splitRel(rel(root, dir)) {
		at = path.Join(at, seg)
		id := dotted(rel(root, at))
		b.add(Module{Path: at, Lang: "py", Kind: "py-package", Identity: id, Parent: parent, Root: ".", Package: id})
		parent = at
	}
	return parent
}

// dotted turns a slash path into a dotted name.
func dotted(p string) string { return strings.ReplaceAll(p, "/", ".") }

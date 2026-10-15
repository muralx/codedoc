package docs

import (
	"path"

	"github.com/muralx/codedoc/internal/parse"
	"github.com/muralx/codedoc/internal/store"
)

// ModuleSize counts the files and lines of a module (see ModuleSizes).
type ModuleSize struct{ Files, Lines int }

// ModuleSizes returns the size of each module of a repo, given all its
// module and file rows. A file belongs to the module row of its language
// family at its directory (a Python file module: at its own path). A
// module's size is that of its own files plus that of every module
// nested under it through parent links: the directories of an npm
// package, the modules and subpackages of a Python package. A module
// below its directory that is not linked to it (another npm package, a Go
// package in a subdirectory) is a unit of its own and is not counted.
// The module tree of overview and the module weight of the gaps ranking
// (spec 11.1) both use it.
func ModuleSizes(mods []store.Module, files []store.File) map[int64]ModuleSize {
	byPath := make(map[string]int64, len(mods))
	parent := make(map[int64]int64, len(mods))
	for _, m := range mods {
		byPath[m.Lang+"\x00"+m.Path] = m.ID
		parent[m.ID] = m.ParentID
	}
	out := make(map[int64]ModuleSize, len(mods))
	for _, f := range files {
		fam := parse.Family(f.Lang)
		id, ok := byPath[fam+"\x00"+f.Path]
		if !ok {
			id, ok = byPath[fam+"\x00"+path.Dir(f.Path)]
		}
		if ok {
			addUp(out, parent, id, f.Lines)
		}
	}
	return out
}

// addUp adds one file of the given lines to a module and to each module
// above it; a parent link that leads nowhere or back ends the walk.
func addUp(out map[int64]ModuleSize, parent map[int64]int64, id int64, lines int) {
	seen := map[int64]bool{}
	for known := true; known && !seen[id]; id, known = next(parent, id) {
		seen[id] = true
		s := out[id]
		s.Files++
		s.Lines += lines
		out[id] = s
	}
}

// next returns the parent of a module and whether it is a known module.
func next(parent map[int64]int64, id int64) (int64, bool) {
	p := parent[id]
	_, known := parent[p]
	return p, known
}

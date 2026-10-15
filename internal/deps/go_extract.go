package deps

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/muralx/codedoc/internal/store"
)

// goSkipDirs are the directories of a module that hold no importable
// API (deps spec section 5).
var goSkipDirs = map[string]bool{"internal": true, "testdata": true, "vendor": true}

// Extract reads the exported API of a Go module (deps spec section 5):
// every non-test .go file outside internal/, testdata/, vendor/, hidden
// directories and nested modules. Each directory is a module named by
// its import path, with the package doc as its doc; a main package
// exports nothing importable and is left out. The root module carries
// the README, else the package doc of the root package. Directories are
// read shallowest first, so a limit cuts the deepest packages.
func (gomod) Extract(p Package, lim Limits) (*Extraction, error) {
	dirs, err := goPackageDirs(p.Dir)
	if err != nil {
		return nil, err
	}
	out := &Extraction{Modules: []Module{{Path: ".", Lang: "go", Kind: "go-package", Identity: p.ID.Name}}}
	b := budget{lim: lim}
	for _, dir := range dirs {
		if b.truncated {
			break
		}
		goPackage(p, dir, &b, out)
	}
	if text, name := readme(p.Dir); text != "" {
		out.Modules[0].Doc, out.Modules[0].DocSource = text, name
	}
	out.Truncated = b.truncated
	return out, nil
}

// goDir is a directory of a module with its Go source files, doc.go
// first, then by name: the order the package comment is looked for in.
type goDir struct {
	rel   string
	files []string
}

// goPackageDirs lists the directories of the module at root that may
// hold an importable package, shallowest first, then by path.
func goPackageDirs(root string) ([]goDir, error) {
	byDir := map[string][]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if p != root && skipGoDir(p, name) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() && strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
			rel, _ := filepath.Rel(root, filepath.Dir(p))
			byDir[filepath.ToSlash(rel)] = append(byDir[filepath.ToSlash(rel)], name)
		}
		return nil
	})
	dirs := make([]goDir, 0, len(byDir))
	for rel, files := range byDir {
		sort.Slice(files, func(i, j int) bool { return docFirst(files[i], files[j]) })
		dirs = append(dirs, goDir{rel: rel, files: files})
	}
	sort.Slice(dirs, func(i, j int) bool { return shallowFirst(dirs[i].rel, dirs[j].rel) })
	return dirs, err
}

// skipGoDir reports whether a directory below a module root holds
// nothing importable from outside the module.
func skipGoDir(dir, name string) bool {
	return goSkipDirs[name] || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || isModuleRoot(dir)
}

// docFirst orders the files of a package: doc.go, then by name.
func docFirst(a, b string) bool {
	if (a == "doc.go") != (b == "doc.go") {
		return a == "doc.go"
	}
	return a < b
}

// shallowFirst orders module-relative directories: the root, then by
// depth, then by path.
func shallowFirst(a, b string) bool {
	if (a == ".") != (b == ".") {
		return a == "."
	}
	if da, db := strings.Count(a, "/"), strings.Count(b, "/"); da != db {
		return da < db
	}
	return a < b
}

// isModuleRoot reports whether a directory holds a go.mod: a nested
// module, which is a package version of its own.
func isModuleRoot(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "go.mod"))
	return err == nil && info.Mode().IsRegular()
}

// goPackage extracts the files of one directory into out as a module,
// unless it is a main package.
func goPackage(p Package, dir goDir, b *budget, out *Extraction) {
	mod := Module{Path: dir.rel, Lang: "go", Kind: "go-package", Identity: path.Join(p.ID.Name, dir.rel)}
	var files []File
	for _, name := range dir.files {
		f, res, ok := apiFile(p.Dir, path.Join(dir.rel, name), b)
		if !ok {
			continue
		}
		if res.ModuleName == "main" {
			return
		}
		if mod.Name == "" {
			mod.Name = res.ModuleName
		}
		if mod.Doc == "" && res.ModuleDoc != nil {
			mod.Doc, mod.DocSource = res.ModuleDoc.Text, f.Path
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		return
	}
	idx := 0
	if dir.rel == "." {
		out.Modules[0].Doc, out.Modules[0].DocSource, out.Modules[0].Name = mod.Doc, mod.DocSource, mod.Name
	} else {
		idx = len(out.Modules)
		out.Modules = append(out.Modules, mod)
	}
	for _, f := range files {
		f.Module = idx
		f.Supers = goSupers(f.Symbols)
		out.Files = append(out.Files, f)
	}
	out.Identities = append(out.Identities, store.RepoIdentity{Lang: "go", Identity: mod.Identity, Path: mod.Path})
}

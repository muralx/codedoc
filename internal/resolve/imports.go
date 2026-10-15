package resolve

import (
	"path"
	"strings"

	"github.com/muralx/codedoc/internal/parse"
	"github.com/muralx/codedoc/internal/store"
)

// Scope limits an import resolution run to what one sync touched.
type Scope struct {
	// Files are the files whose imports are resolved again.
	Files []int64
	// Modules are the source modules whose outgoing module edges are
	// rebuilt from the imports of their files; each needs ID, RepoID,
	// Lang (family) and Path.
	Modules []store.Module
}

// Imports resolves import rows to module rows and rebuilds module edges
// in one transaction, reading and writing through the same batch. With a
// nil scope every import of the workspace is resolved, against modules
// and identities preloaded into memory, and every module edge rebuilt;
// otherwise only the scoped files and the edges of the scoped modules.
// order lists the workspace's repo names in config order: when several
// repos provide an identity, the importing repo wins, else the first of
// them in that order (spec 7.3).
func Imports(s *store.Store, order []string, scope *Scope) error {
	return s.Batch(func(b *store.Batch) error {
		r := newResolver(b, order)
		if scope == nil {
			return r.all()
		}
		return r.scoped(*scope)
	})
}

// all resolves every import and replaces every module edge.
func (r *resolver) all() error {
	if err := r.preload(); err != nil {
		return err
	}
	imps, err := r.b.Imports(nil)
	if err != nil {
		return err
	}
	if err := r.resolveEach(imps); err != nil {
		return err
	}
	edges, err := r.moduleEdges(imps)
	if err != nil {
		return err
	}
	return r.b.ReplaceModuleEdges(edges)
}

// scoped resolves the imports of sc.Files, then rebuilds the edges
// leaving sc.Modules from the imports of their files.
func (r *resolver) scoped(sc Scope) error {
	files := sc.Files
	if files == nil {
		files = []int64{}
	}
	imps, err := r.b.Imports(files)
	if err != nil {
		return err
	}
	if err := r.resolveEach(imps); err != nil {
		return err
	}
	srcs := make([]int64, 0, len(sc.Modules))
	var edges []store.ModuleEdge
	for _, m := range sc.Modules {
		in, err := r.importsOf(m)
		if err != nil {
			return err
		}
		srcs = append(srcs, m.ID)
		edges = appendEdges(edges, m, in)
	}
	return r.b.ReplaceModuleEdgesFrom(srcs, edges)
}

// importsOf returns the imports of the files of a module: those directly
// in its directory, or for Python the module's file, a package's being
// its __init__.py.
func (r *resolver) importsOf(m store.Module) ([]store.Import, error) {
	if m.Lang != "py" {
		return r.b.ImportsInDir(m.RepoID, m.Path)
	}
	p := m.Path
	if !strings.HasSuffix(p, ".py") {
		p = path.Join(p, "__init__.py")
	}
	return r.b.ImportsAtPath(m.RepoID, p)
}

// resolveEach resolves imps in place and stores the ones that changed.
func (r *resolver) resolveEach(imps []store.Import) error {
	for i, imp := range imps {
		id, err := r.resolve(imp)
		if err != nil {
			return err
		}
		if id != imp.ResolvedModuleID {
			imps[i].ResolvedModuleID = id
			if err := r.b.SetImportModule(imp.RowID, id); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolve returns the module an import names, or 0.
func (r *resolver) resolve(imp store.Import) (int64, error) {
	switch parse.Family(imp.Lang) {
	case "go":
		return r.goImport(imp)
	case "ts":
		if isRelative(imp.Raw) {
			target := path.Join(path.Dir(imp.Path), imp.Raw)
			if target == ".." || strings.HasPrefix(target, "../") {
				return 0, nil
			}
			return r.tsPath(imp.RepoID, target)
		}
		return r.tsPackage(imp)
	case "py":
		return r.pyImport(imp)
	case "java":
		return r.javaImport(imp)
	}
	return 0, nil
}

// goImport matches the import path exactly against the package
// identities of the repos whose go.mod module path is the longest prefix
// of it.
func (r *resolver) goImport(imp store.Import) (int64, error) {
	segs := strings.Split(imp.Raw, "/")
	for i := len(segs); i > 0; i-- {
		repos, err := r.preferred("go", strings.Join(segs[:i], "/"), imp.RepoID)
		if err != nil {
			return 0, err
		}
		for _, repo := range repos {
			if m, err := r.moduleByIdentity(repo.ID, "go", imp.Raw); m.ID != 0 || err != nil {
				return m.ID, err
			}
		}
	}
	return 0, nil
}

// tsPackage resolves a bare specifier: the package name (scoped or not)
// selects the providing repo, and a subpath is resolved like a relative
// path from the package root.
func (r *resolver) tsPackage(imp store.Import) (int64, error) {
	pkg, sub := splitPackage(imp.Raw)
	repos, err := r.preferred("ts", pkg, imp.RepoID)
	if err != nil {
		return 0, err
	}
	for _, repo := range repos {
		root, err := r.moduleByIdentity(repo.ID, "ts", pkg)
		if err != nil {
			return 0, err
		}
		if root.ID == 0 {
			continue
		}
		if id, err := r.tsPath(repo.ID, path.Join(root.Path, sub)); id != 0 || err != nil {
			return id, err
		}
	}
	return 0, nil
}

// tsPath returns the module a repo-relative import target lands in,
// trying the target as a file before trying it as a directory, as Node
// and TypeScript do: the module of the directory holding the target file
// when such a file exists (with any TS or JS extension), else the
// directory module the target names, directly or through its index file.
// A .js, .ts or similar extension is dropped first.
func (r *resolver) tsPath(repoID int64, target string) (int64, error) {
	stem, hadExt := stripExt(target)
	if path.Base(stem) == "index" {
		return r.moduleAt(repoID, "ts", path.Dir(stem))
	}
	if !hadExt {
		dir, err := r.moduleAt(repoID, "ts", stem)
		if err != nil {
			return 0, err
		}
		if dir != 0 {
			if file, err := r.fileExists(repoID, stem); err != nil || !file {
				return dir, err
			}
		}
	}
	return r.moduleAt(repoID, "ts", path.Dir(stem))
}

func isRelative(spec string) bool {
	return spec == "." || spec == ".." || strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../")
}

// splitPackage splits a bare specifier into the package name (one
// segment, or two for a @scope) and the subpath.
func splitPackage(spec string) (pkg, sub string) {
	n := 1
	if strings.HasPrefix(spec, "@") {
		n = 2
	}
	segs := strings.SplitN(spec, "/", n+1)
	if len(segs) <= n {
		return spec, ""
	}
	return strings.Join(segs[:n], "/"), segs[n]
}

// stripExt drops a TypeScript or JavaScript extension.
func stripExt(p string) (string, bool) {
	if parse.Family(parse.LanguageForPath(p)) == "ts" {
		return strings.TrimSuffix(p, path.Ext(p)), true
	}
	return p, false
}

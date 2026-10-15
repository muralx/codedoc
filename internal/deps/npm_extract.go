package deps

import (
	"path"
	"strings"

	"github.com/muralx/codedoc/internal/store"
)

// Extract reads the declarations of an npm package (deps spec section
// 5). Every entry point (see npmEntries) is a module named after its
// import specifier; the files reached from it through relative imports,
// re-exports and reference directives belong to the first module that
// reaches them, an entry file to its own. A dual package ships every
// declaration file twice (x.d.ts for import, x.d.cts for require): such
// twins are one file, read once (see stem). The root module carries the
// README. A package without declarations yields the root module alone.
// With p.Only the walk starts from the declaration files of those
// modules instead of the entry files (see startFiles).
func (npm) Extract(p Package, lim Limits) (*Extraction, error) {
	pj, err := readPackageJSON(p.Dir)
	if err != nil {
		return nil, err
	}
	typesPJ := pj
	if p.TypesDir != "" {
		if typesPJ, err = readPackageJSON(p.TypesDir); err != nil {
			return nil, err
		}
	}
	w := &npmWalk{root: p.root(), name: p.ID.Name, budget: budget{lim: lim}, module: map[string]int{}}
	w.out.Modules = []Module{{Path: ".", Lang: "ts", Kind: "npm-package", Identity: p.ID.Name}}
	w.out.Modules[0].Doc, w.out.Modules[0].DocSource = readme(p.Dir)
	entries := npmEntries(w.root, typesPJ)
	for _, e := range entries {
		w.entry(e)
	}
	for _, file := range startFiles(w.root, entries, p.Only) {
		w.follow(file)
	}
	w.out.Only = p.Only
	w.out.Truncated = w.budget.truncated
	w.out.Exports = w.graph.build(w.out.Files)
	return &w.out, nil
}

// startFiles returns the files extraction starts from: the entry files,
// or for a package read partially the declaration files of the modules
// only (see ambientFile), the entry files left unread.
func startFiles(root string, entries []npmEntry, only []string) []string {
	var out []string
	if only == nil {
		for _, e := range entries {
			out = append(out, e.File)
		}
		return out
	}
	for _, mod := range only {
		if file := ambientFile(root, mod); file != "" {
			out = append(out, file)
		}
	}
	return out
}

// npmWalk follows the declaration files of one package.
type npmWalk struct {
	root, name string
	budget     budget
	out        Extraction
	// module maps the stem of a file to the index of its module.
	module map[string]int
	// read holds the stems of the files extracted or found unreadable.
	read map[string]bool
	// graph collects what the files re-export from each other.
	graph exportGraph
}

// declExts are the extensions of declaration files, longest first.
var declExts = []string{".d.ts", ".d.mts", ".d.cts", ".ts", ".mts", ".cts", ".tsx"}

// stem returns a declaration file's path without its extension, the
// same for the ESM and CommonJS twins of one declaration file.
func stem(file string) string {
	for _, ext := range declExts {
		if s, ok := strings.CutSuffix(file, ext); ok {
			return s
		}
	}
	return file
}

// entry gives an entry point its module, a new one unless an earlier
// entry names the same file, and records its import specifier.
func (w *npmWalk) entry(e npmEntry) {
	sub := strings.TrimPrefix(strings.TrimPrefix(e.Subpath, "."), "/")
	identity, modPath := w.name, "."
	if sub != "" {
		identity, modPath = w.name+"/"+sub, sub
	}
	idx, ok := w.module[stem(e.File)]
	switch {
	case ok:
		modPath = w.out.Modules[idx].Path
	case sub == "":
		w.module[stem(e.File)] = 0
	default:
		w.module[stem(e.File)] = len(w.out.Modules)
		w.out.Modules = append(w.out.Modules, Module{Path: modPath, Lang: "ts", Kind: "npm-export", Identity: identity})
	}
	w.out.Identities = append(w.out.Identities, store.RepoIdentity{Lang: "ts", Identity: identity, Path: modPath})
	w.graph.entry(identity, stem(e.File))
}

// follow extracts a file and, breadth first, the files it reaches that
// no module holds yet, until the budget is spent.
func (w *npmWalk) follow(start string) {
	if w.read == nil {
		w.read = map[string]bool{}
	}
	queue := []string{start}
	for len(queue) > 0 && !w.budget.truncated {
		rel := queue[0]
		queue = queue[1:]
		if w.read[stem(rel)] {
			continue
		}
		w.read[stem(rel)] = true
		f, res, ok := apiFile(w.root, rel, &w.budget)
		if !ok {
			continue
		}
		f.Module = w.module[stem(rel)]
		specs := referencePaths(w.root, rel)
		for _, imp := range res.Imports {
			specs = append(specs, imp.Raw)
		}
		targets := map[string]string{}
		for _, spec := range specs {
			next := relativeDecl(w.root, rel, spec)
			if next == "" {
				continue
			}
			targets[spec] = next
			if _, held := w.module[stem(next)]; !held {
				w.module[stem(next)] = f.Module
			}
			queue = append(queue, next)
		}
		f.Supers = tsSupers(res, targets)
		w.graph.file(stem(rel), res.Imports, targets)
		w.out.Files = append(w.out.Files, f)
	}
}

// relativeDecl resolves a relative import specifier of the file from to
// a declaration file of the package; "" for a bare specifier or a file
// that does not exist.
func relativeDecl(root, from, spec string) string {
	if !strings.HasPrefix(spec, "./") && !strings.HasPrefix(spec, "../") && spec != "." && spec != ".." {
		return ""
	}
	return declFile(root, path.Join(path.Dir(from), spec))
}

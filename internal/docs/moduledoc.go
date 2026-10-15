package docs

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/parse"
)

// ModuleRef is a module whose doc is looked up: its repo-relative path
// (a directory, or the file of a Python file module), language family
// and kind (resolve.Module kinds).
type ModuleRef struct {
	Path, Lang, Kind string
}

// ModuleDoc is a module's doc text and the repo-relative file it comes
// from; both are empty when the module has none.
type ModuleDoc struct {
	Text, Source string
}

// State is ok for a module with a doc, else missing (module staleness
// is not tracked).
func (d ModuleDoc) State() State {
	if d.Text == "" {
		return Missing
	}
	return OK
}

// Lookup finds module docs (spec 7.2 and 10) in the repo at Root.
// Known holds the module docs of the files extracted by the current
// index run, nil for a file without one or deleted, so they are not
// parsed again.
type Lookup struct {
	Root  string
	Known map[string]*extract.Doc
}

// Find returns the doc of a module, by language:
//
//   - Go: the package comment of doc.go, else the first package comment of
//     the directory's other non-test files in name order;
//   - TS/JS: README.md in the directory (its ## Purpose section, else the
//     text under its first heading), else a leading /** */ of an index
//     file;
//   - Python: the __init__.py docstring of a package, the module
//     docstring of a file module;
//   - Java: the Javadoc of package-info.java.
//
// cur is the doc stored for the module, nil to look everywhere. For a
// Go package whose source file the run did not touch, only the files the
// run extracted can change the answer: the others ranked below cur
// before (or carried no doc), so they are not read again.
func (l Lookup) Find(m ModuleRef, cur *ModuleDoc) (ModuleDoc, error) {
	switch m.Lang {
	case "go":
		if _, touched := l.Known[cur.sourceOrNone()]; cur != nil && !touched {
			return l.betterGo(m.Path, *cur), nil
		}
		return l.first(goCandidates(l.Root, m.Path))
	case "ts":
		if d, err := readme(l.Root, m.Path); err != nil || d.Text != "" {
			return d, err
		}
		return l.first(indexFiles(l.Root, m.Path))
	case "py":
		if m.Kind == "py-module" {
			return l.first([]string{m.Path}, nil)
		}
		return l.first([]string{path.Join(m.Path, "__init__.py")}, nil)
	case "java":
		return l.first([]string{path.Join(m.Path, "package-info.java")}, nil)
	}
	return ModuleDoc{}, nil
}

// sourceOrNone is the source of a stored doc; a name no file has when
// there is none.
func (d *ModuleDoc) sourceOrNone() string {
	if d == nil || d.Source == "" {
		return "\x00"
	}
	return d.Source
}

// betterGo applies the incremental Go rule: among cur and the files of
// dir the run extracted, the first in doc order that carries a package
// comment.
func (l Lookup) betterGo(dir string, cur ModuleDoc) ModuleDoc {
	best := cur
	for p, d := range l.Known {
		if path.Dir(p) != dir || !isGoSource(path.Base(p)) || d == nil || d.Text == "" {
			continue
		}
		if best.Source == "" || goBefore(path.Base(p), path.Base(best.Source)) {
			best = ModuleDoc{Text: d.Text, Source: p}
		}
	}
	return best
}

// first returns the doc of the first of paths that carries one.
func (l Lookup) first(paths []string, err error) (ModuleDoc, error) {
	if err != nil {
		return ModuleDoc{}, err
	}
	for _, p := range paths {
		d, err := l.docOf(p)
		if err != nil {
			return ModuleDoc{}, err
		}
		if d != nil && d.Text != "" {
			return ModuleDoc{Text: d.Text, Source: p}, nil
		}
	}
	return ModuleDoc{}, nil
}

// docOf returns the module doc a file carries: from Known, else by
// extracting it from disk; nil when the file is missing.
func (l Lookup) docOf(p string) (*extract.Doc, error) {
	if d, ok := l.Known[p]; ok {
		return d, nil
	}
	lang := parse.LanguageForPath(p)
	ex, ok := extract.For(lang)
	if !ok {
		return nil, nil
	}
	src, err := os.ReadFile(filepath.Join(l.Root, filepath.FromSlash(p)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	res, err := ex.Extract(p, src)
	if err != nil {
		return nil, nil // a file the extractor cannot handle carries no doc
	}
	return res.ModuleDoc, nil
}

// goCandidates lists the non-test Go files of dir in doc order.
func goCandidates(root, dir string) ([]string, error) {
	names, err := listDir(root, dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, n := range names {
		if isGoSource(n) {
			out = append(out, path.Join(dir, n))
		}
	}
	sort.Slice(out, func(i, j int) bool { return goBefore(path.Base(out[i]), path.Base(out[j])) })
	return out, nil
}

func isGoSource(name string) bool {
	return strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
}

// goBefore orders Go files for the package comment: doc.go first, then
// by name.
func goBefore(a, b string) bool {
	if (a == "doc.go") != (b == "doc.go") {
		return a == "doc.go"
	}
	return a < b
}

// indexFiles lists the index.* files of a TS/JS directory by name.
func indexFiles(root, dir string) ([]string, error) {
	names, err := listDir(root, dir)
	var out []string
	for _, n := range names {
		if strings.HasPrefix(n, "index.") && parse.Family(parse.LanguageForPath(n)) == "ts" {
			out = append(out, path.Join(dir, n))
		}
	}
	return out, err
}

// listDir returns the sorted file names of a repo directory; none when
// it is gone.
func listDir(root, dir string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	var out []string
	for _, e := range entries {
		if e.Type().IsRegular() {
			out = append(out, e.Name())
		}
	}
	return out, err
}

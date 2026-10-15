package write

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/parse"
)

// extractSource runs the extractor of lang over src.
func extractSource(lang, path string, src []byte) (*extract.FileResult, error) {
	ex, ok := extract.For(lang)
	if !ok {
		return nil, fmt.Errorf("write: no extractor for %q", lang)
	}
	return ex.Extract(path, src)
}

// findSymbol extracts src and returns the one symbol matching t's
// qualified name and signature hash.
func findSymbol(t Target, src []byte) (extract.Symbol, *extract.FileResult, error) {
	res, err := extractSource(t.Lang, t.extractPath(), src)
	if err != nil {
		return extract.Symbol{}, nil, err
	}
	var found []extract.Symbol
	for _, s := range res.Symbols {
		if s.Qualified == t.Qualified && (t.SigHash == "" || s.SigHash == t.SigHash) {
			found = append(found, s)
		}
	}
	switch len(found) {
	case 0:
		return extract.Symbol{}, res, fmt.Errorf("write: symbol %s not found in %s", t.Qualified, t.Path)
	case 1:
		return found[0], res, nil
	}
	return extract.Symbol{}, res, fmt.Errorf("write: %s is ambiguous in %s; give its signature hash", t.Qualified, t.Path)
}

// moduleFileDoc extracts a module doc file (doc.go, __init__.py,
// package-info.java) and returns its module doc.
func moduleFileDoc(lang, path string, src []byte) (*extract.Doc, int, error) {
	res, err := extractSource(lang, path, src)
	if err != nil {
		return nil, 0, err
	}
	return res.ModuleDoc, res.ParseErrors, nil
}

// readExisting reads a repo-relative file through readSource, reporting
// whether it exists.
func readExisting(root, path string) ([]byte, bool, error) {
	src, err := readSource(filepath.Join(root, path))
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	return src, err == nil, err
}

// packageName returns the package name declared by the source files of
// lang (by extension ext) in a module directory, read in name order. Go
// test files are used only when no other file declares the name, with the
// external test package's _test suffix removed.
func packageName(root, dir, lang, ext string) (string, error) {
	entries, err := os.ReadDir(filepath.Join(root, dir))
	if err != nil {
		return "", err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ext) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	fallback := ""
	for _, n := range names {
		src, err := os.ReadFile(filepath.Join(root, dir, n))
		if err != nil {
			return "", err
		}
		res, err := extractSource(lang, filepath.ToSlash(filepath.Join(dir, n)), src)
		if err != nil || res.ModuleName == "" {
			continue
		}
		if lang == parse.Go && strings.HasSuffix(n, "_test.go") {
			fallback = strings.TrimSuffix(res.ModuleName, "_test")
			continue
		}
		return res.ModuleName, nil
	}
	if fallback != "" {
		return fallback, nil
	}
	return "", fmt.Errorf("write: no %s file in %s declares a package", lang, dir)
}

// topLevelStart returns the start of the first top-level node of kind in
// src, parsed as lang.
func topLevelStart(lang string, src []byte, kind string) (uint, bool, error) {
	tree, err := parse.Parse(lang, src)
	if err != nil {
		return 0, false, err
	}
	defer tree.Close()
	root := tree.Root()
	for i := uint(0); i < root.NamedChildCount(); i++ {
		if n := root.NamedChild(i); n.Kind() == kind {
			return n.StartByte(), true, nil
		}
	}
	return 0, false, nil
}

// moduleFile returns the repo-relative path of a file in a module dir.
func moduleFile(dir, name string) string {
	return filepath.ToSlash(filepath.Join(dir, name))
}

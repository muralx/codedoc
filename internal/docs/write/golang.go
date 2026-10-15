package write

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/parse"
)

// goLang writes // doc comments and doc.go package comments.
type goLang struct{}

// goDirective matches directive comments (//go:noinline, //lint:ignore),
// which belong to the declaration and survive a doc rewrite.
var goDirective = regexp.MustCompile(`^\s*//[a-z0-9]+:\S`)

func (goLang) symbolDoc(src []byte, sym extract.Symbol, text string) ([]byte, string, error) {
	indent, ok := lineIndent(src, sym.Span.StartByte)
	if !ok {
		return nil, "", fmt.Errorf("write: %s does not start its line", sym.Qualified)
	}
	text = prefixWord(text, sym.Name)
	if sym.Doc == nil {
		if at := directivesStart(src, sym.Span.StartByte); at != lineStart(src, sym.Span.StartByte) {
			lines := append(slashLines(text, indent), "//")
			return insertAbove(src, at, lines, indent), text, nil
		}
	}
	return goComment(src, sym.Doc, sym.Span.StartByte, text, indent), text, nil
}

// directivesStart returns the start of the run of directive comment
// lines (//go:noinline) directly above the line holding pos, or the
// start of that line when there are none: a new doc goes above them, as
// gofmt places directives at the end of a doc comment.
func directivesStart(src []byte, pos uint) uint {
	at := lineStart(src, pos)
	for at > 0 {
		prev := lineStart(src, at-1)
		if !goDirective.Match(src[prev : at-1]) {
			break
		}
		at = prev
	}
	return at
}

// goComment replaces doc, keeping its directives, or inserts the comment
// above the line holding at.
func goComment(src []byte, doc *extract.Doc, at uint, text, indent string) []byte {
	lines := slashLines(text, indent)
	if doc == nil {
		return insertAbove(src, at, lines, indent)
	}
	var directives []string
	for _, l := range strings.Split(string(src[doc.Span.StartByte:doc.Span.EndByte]), "\n") {
		if goDirective.MatchString(l) {
			directives = append(directives, strings.TrimSpace(l))
		}
	}
	if len(directives) > 0 {
		lines = append(append(lines, "//"), directives...)
	}
	return replaceSpan(src, doc.Span.StartByte, doc.Span.EndByte, lines, indent)
}

// moduleDoc writes the package comment into the file goDocFile picks,
// creating doc.go with the package clause when no file has one, so a
// package never gains a second package comment. The other files that
// carry one are left alone and reported in Others.
func (goLang) moduleDoc(t Target, text string) (Edit, error) {
	path, others, err := goDocFile(t.Root, t.ModuleDir)
	if err != nil {
		return Edit{}, err
	}
	src, existed, err := readExisting(t.Root, path)
	if err != nil {
		return Edit{}, err
	}
	if !existed {
		pkg, err := packageName(t.Root, t.ModuleDir, parse.Go, ".go")
		if err != nil {
			return Edit{}, err
		}
		text = prefixWord(text, "Package "+pkg)
		out := strings.Join(slashLines(text, ""), "\n") + "\npackage " + pkg + "\n"
		return Edit{Path: path, After: []byte(out), Want: text}, nil
	}
	res, err := extractSource(parse.Go, path, src)
	if err != nil {
		return Edit{}, err
	}
	at, ok, err := topLevelStart(parse.Go, src, "package_clause")
	if err != nil {
		return Edit{}, err
	}
	if !ok {
		return Edit{}, fmt.Errorf("write: %s has no package clause", path)
	}
	text = prefixWord(text, "Package "+res.ModuleName)
	out := goComment(src, res.ModuleDoc, at, text, "")
	return Edit{Path: path, Existed: true, Before: src, After: out, Want: text, Others: others}, nil
}

// goDocFile picks the file of dir that receives the package comment:
// doc.go when it exists, else the first non-test Go file by name that
// carries a package comment (the one the index reads), else doc.go to
// be created. others lists the other non-test files carrying one.
func goDocFile(root, dir string) (path string, others []string, err error) {
	docGo := moduleFile(dir, "doc.go")
	files, err := goSources(root, dir)
	if err != nil {
		return "", nil, err
	}
	var commented []string
	for _, p := range files {
		if p == docGo {
			continue
		}
		src, err := readSource(filepath.Join(root, p))
		if err != nil {
			return "", nil, err
		}
		if d, _, err := moduleFileDoc(parse.Go, p, src); err == nil && d != nil && d.Text != "" {
			commented = append(commented, p)
		}
	}
	switch {
	case slices.Contains(files, docGo):
		return docGo, commented, nil
	case len(commented) > 0:
		return commented[0], commented[1:], nil
	}
	return docGo, nil, nil
}

// goSources lists the non-test Go files of dir by name.
func goSources(root, dir string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(root, dir))
	var out []string
	for _, e := range entries {
		if n := e.Name(); !e.IsDir() && strings.HasSuffix(n, ".go") && !strings.HasSuffix(n, "_test.go") {
			out = append(out, moduleFile(dir, n))
		}
	}
	return out, err
}

func (goLang) readModuleDoc(path string, src []byte) (*extract.Doc, int, error) {
	return moduleFileDoc(parse.Go, path, src)
}

// Package java extracts symbols, docs, references and imports from Java
// source files.
package java

import (
	"path"
	"strings"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/parse"
)

func init() { extract.Register(parse.Java, New()) }

// Extractor is the Java extractor.
type Extractor struct{}

// New returns a Java extractor.
func New() *Extractor { return &Extractor{} }

// Extract parses a Java file and returns its declarations, references and
// imports. Qualified names join nested types and members with dots
// (Outer.Inner.method); constructors are named <init>. Methods and
// constructors carry a signature hash (spec 5.1). Test methods are
// recognized only in test files (see isTestFile).
func (*Extractor) Extract(filePath string, src []byte) (*extract.FileResult, error) {
	tree, err := parse.Parse(parse.Java, src)
	if err != nil {
		return nil, err
	}
	defer tree.Close()
	root := tree.Root()
	w := &walker{
		src: src, res: &extract.FileResult{},
		testFile: isTestFile(filePath),
	}
	for i := uint(0); i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		if n.Kind() == "package_declaration" {
			w.packageDecl(n, path.Base(filePath) == "package-info.java")
			continue
		}
		w.member(n, "", false)
	}
	extract.HashContainers(w.res.Symbols, nil)
	extract.UniqueQualified(w.res.Symbols)
	w.refs(root)
	w.res.ParseErrors = extract.CountErrors(root)
	return w.res, nil
}

// isTestFile matches files under src/test/ or a javatests/ directory and
// files named *Test.java, *Tests.java or *IT.java; their methods are tests
// when they carry a JUnit test annotation.
func isTestFile(p string) bool {
	p = "/" + p
	base := path.Base(p)
	return strings.Contains(p, "/src/test/") || strings.Contains(p, "/javatests/") ||
		strings.HasSuffix(base, "Test.java") || strings.HasSuffix(base, "Tests.java") ||
		strings.HasSuffix(base, "IT.java")
}

// packageDecl records the package name and, in package-info.java, the
// Javadoc above the declaration as the module doc.
func (w *walker) packageDecl(n parse.Node, info bool) {
	for i := uint(0); i < n.NamedChildCount(); i++ {
		if c := n.NamedChild(i); c.Kind() == "scoped_identifier" || c.Kind() == "identifier" {
			w.res.ModuleName = c.Text(w.src)
		}
	}
	if info {
		w.res.ModuleDoc = w.docFor(n)
	}
}

// docFor returns the Javadoc comment directly before n, or nil. Modifiers
// and annotations belong to the declaration, so the comment precedes
// them.
func (w *walker) docFor(n parse.Node) *extract.Doc {
	c := n.PrevSibling()
	if c == nil || c.Kind() != "block_comment" {
		return nil
	}
	raw := c.Text(w.src)
	if !strings.HasPrefix(raw, "/**") || raw == "/**/" {
		return nil
	}
	text := javadocText(raw)
	if text == "" {
		return nil
	}
	return &extract.Doc{Text: text, Span: extract.SpanOf(c)}
}

// javadocText strips /**, */ and the leading " * " of every line.
func javadocText(raw string) string {
	body := strings.TrimSuffix(strings.TrimPrefix(raw, "/**"), "*/")
	lines := strings.Split(body, "\n")
	for i, l := range lines {
		l = strings.TrimSpace(l)
		l = strings.TrimPrefix(l, "*")
		lines[i] = strings.TrimRight(strings.TrimPrefix(l, " "), " \t\r")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

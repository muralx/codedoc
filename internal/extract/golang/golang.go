// Package golang extracts symbols, docs, references and imports from Go
// source files.
package golang

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/parse"
)

func init() { extract.Register(parse.Go, New()) }

// Extractor is the Go extractor.
type Extractor struct{}

// New returns a Go extractor.
func New() *Extractor { return &Extractor{} }

// Extract parses a Go file and returns its declarations, references and
// imports. Test functions are recognized only in files whose path ends in
// _test.go.
func (*Extractor) Extract(path string, src []byte) (*extract.FileResult, error) {
	tree, err := parse.Parse(parse.Go, src)
	if err != nil {
		return nil, err
	}
	defer tree.Close()
	root := tree.Root()
	w := &walker{src: src, testFile: strings.HasSuffix(path, "_test.go"), res: &extract.FileResult{}}
	w.walk(root)
	extract.HashContainers(w.res.Symbols, w.embedded)
	extract.UniqueQualified(w.res.Symbols)
	w.refs(root)
	w.res.ParseErrors = extract.CountErrors(root)
	return w.res, nil
}

// ExtractAPI parses a Go file and returns its exported API only:
// exported top-level declarations, and the exported fields and methods
// of exported types (the receiver type may be declared in another file
// of the package). It returns the package doc and name, but no
// references and no imports.
func (*Extractor) ExtractAPI(_ string, src []byte) (*extract.FileResult, error) {
	tree, err := parse.Parse(parse.Go, src)
	if err != nil {
		return nil, err
	}
	defer tree.Close()
	root := tree.Root()
	w := &walker{src: src, res: &extract.FileResult{}}
	w.walk(root)
	extract.HashContainers(w.res.Symbols, w.embedded)
	extract.UniqueQualified(w.res.Symbols)
	kept := w.res.Symbols[:0]
	for _, s := range w.res.Symbols {
		if s.Visibility == extract.Public && (s.Container == "" || visibility(s.Container) == extract.Public) {
			kept = append(kept, s)
		}
	}
	w.res.Symbols = kept
	w.res.ParseErrors = extract.CountErrors(root)
	return w.res, nil
}

// symbol fills the fields every declaration shares. The span, signature
// and body hash cover n; callers narrow the signature where a body exists.
func (w *walker) symbol(kind extract.Kind, name, container string, n parse.Node) extract.Symbol {
	q := name
	if container != "" {
		q = container + "." + name
	}
	s := extract.Symbol{
		Kind: kind, Name: name, Qualified: q, Container: container,
		Span: extract.SpanOf(n), Visibility: visibility(name), BodyHash: extract.NodeHash(w.src, n),
	}
	s.Signature, s.SigText = extract.SignatureSpan(n, n.EndByte(), w.src)
	return s
}

func (w *walker) funcSymbol(n parse.Node) extract.Symbol {
	name := w.field(n, "name")
	s := w.symbol(extract.KindFunction, name, "", n)
	s.IsTest = w.testFile && isTestName(name)
	w.finishCallable(&s, n)
	return s
}

func (w *walker) methodSymbol(n parse.Node) extract.Symbol {
	s := w.symbol(extract.KindMethod, w.field(n, "name"), w.receiverType(n), n)
	w.finishCallable(&s, n)
	return s
}

// finishCallable narrows the signature to the text before the body, hashes
// the signature text with the body and attaches the doc comment.
func (w *walker) finishCallable(s *extract.Symbol, n parse.Node) {
	if body := n.ChildByField("body"); body != nil {
		s.Signature, s.SigText = extract.SignatureSpan(n, body.StartByte(), w.src)
		s.BodyHash = extract.CodeHash(s.SigText, w.src, body, body.StartByte(), body.EndByte())
	}
	s.Doc = w.docFor(n)
}

// receiverType returns the receiver's type name without pointer and type
// arguments: "*List[T]" gives "List".
func (w *walker) receiverType(n parse.Node) string {
	recv := n.ChildByField("receiver")
	if recv == nil || recv.NamedChildCount() == 0 {
		return ""
	}
	return baseTypeName(w.field(recv.NamedChild(0), "type"))
}

// baseTypeName strips pointers, type arguments and a package qualifier.
func baseTypeName(t string) string {
	t = strings.TrimLeft(t, "*( ")
	t, _, _ = strings.Cut(t, "[")
	t = strings.TrimRight(t, ") ")
	if i := strings.LastIndex(t, "."); i >= 0 {
		t = t[i+1:]
	}
	return t
}

// field returns the text of a named field child, or "" when absent.
func (w *walker) field(n parse.Node, name string) string {
	if c := n.ChildByField(name); c != nil {
		return c.Text(w.src)
	}
	return ""
}

func visibility(name string) extract.Visibility {
	r, _ := utf8.DecodeRuneInString(name)
	if unicode.IsUpper(r) {
		return extract.Public
	}
	return extract.Package
}

// isTestName applies the go test naming rule: a Test, Benchmark, Fuzz or
// Example prefix followed by the end of the name or a rune that is not a
// lower-case letter, so TestFoo and Test_foo are tests and Testify is not.
func isTestName(name string) bool {
	for _, p := range []string{"Test", "Benchmark", "Fuzz", "Example"} {
		if rest, ok := strings.CutPrefix(name, p); ok {
			r, _ := utf8.DecodeRuneInString(rest)
			return rest == "" || !unicode.IsLower(r)
		}
	}
	return false
}

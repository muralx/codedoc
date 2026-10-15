package golang_test

import (
	"testing"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/extract/golang"
	"github.com/muralx/codedoc/internal/extract/golden"
)

func TestGolden(t *testing.T) {
	golden.RunFixtures(t, golang.New(), golden.Sources(t, "go-", ".go"))
}

func TestRegistered(t *testing.T) {
	if _, ok := extract.For("go"); !ok {
		t.Fatal("go extractor not registered")
	}
}

func extractSrc(t *testing.T, path, src string) *extract.FileResult {
	t.Helper()
	res, err := golang.New().Extract(path, []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func byQualified(res *extract.FileResult) map[string]extract.Symbol {
	m := map[string]extract.Symbol{}
	for _, s := range res.Symbols {
		m[s.Qualified] = s
	}
	return m
}

const edgeCases = `package p

import "io"

var a = 1 // trailing, not a doc
func AfterTrailing() {}

// Grouped holds two constants.
const (
	// One is documented.
	One = 1
	two, Three = 2, 3
)

var _ io.Reader = (*List[int])(nil)

type (
	// A is an alias.
	A = int
	b struct {
		io.Reader
		*List[int]
		x, Y int ` + "`json:\"y\"`" + `
	}
)

/*
List is generic.

  Second paragraph.
*/
type List[T any] struct{ items []T }

//go:noinline
// Len counts.
func (l *List[T]) Len(
	extra int,
) int {
	return len(l.items)
}

type Reader interface {
	io.Closer
	Read(p []byte) (int, error)
}
`

func TestEdgeCases(t *testing.T) {
	syms := byQualified(extractSrc(t, "p/p.go", edgeCases))
	for _, c := range []struct {
		qualified, kind, doc, sig string
		vis                       extract.Visibility
	}{
		{"AfterTrailing", "function", "", "func AfterTrailing()", extract.Public},
		{"One", "const", "One is documented.", "One = 1", extract.Public},
		{"two", "const", "", "two, Three = 2, 3", extract.Package},
		{"Three", "const", "", "two, Three = 2, 3", extract.Public},
		{"A", "type", "A is an alias.", "A = int", extract.Public},
		{"b", "struct", "", "b struct", extract.Package},
		{"b.Reader", "field", "", "io.Reader", extract.Public},
		{"b.List", "field", "", "*List[int]", extract.Public},
		{"b.x", "field", "", "x, Y int", extract.Package},
		{"b.Y", "field", "", "x, Y int", extract.Public},
		{"List", "struct", "List is generic.\n\n Second paragraph.", "type List[T any] struct", extract.Public},
		{"List.items", "field", "", "items []T", extract.Package},
		{"List.Len", "method", "Len counts.", "func (l *List[T]) Len(extra int,) int", extract.Public},
		{"Reader.Read", "method", "", "Read(p []byte) (int, error)", extract.Public},
	} {
		s, ok := syms[c.qualified]
		if !ok {
			t.Errorf("%s: missing", c.qualified)
			continue
		}
		doc := ""
		if s.Doc != nil {
			doc = s.Doc.Text
		}
		if string(s.Kind) != c.kind || doc != c.doc || s.SigText != c.sig || s.Visibility != c.vis {
			t.Errorf("%s: got kind %s doc %q sig %q vis %s", c.qualified, s.Kind, doc, s.SigText, s.Visibility)
		}
	}
	if _, ok := syms["_"]; ok {
		t.Error("blank identifiers must be skipped")
	}
	if len(syms) != 16 {
		t.Errorf("got %d symbols: %v", len(syms), syms)
	}
}

func TestParseErrorsAndTests(t *testing.T) {
	res := extractSrc(t, "x_test.go", "package x\n\nfunc TestA(t *T) {}\nfunc helper() {}\nfunc BenchmarkB() {}\nfunc F( {\n")
	if res.ParseErrors == 0 {
		t.Error("parse errors not counted")
	}
	syms := byQualified(res)
	if !syms["TestA"].IsTest || syms["helper"].IsTest || !syms["BenchmarkB"].IsTest {
		t.Errorf("test detection: %+v", syms)
	}
	if extractSrc(t, "x.go", "package x\nfunc TestA() {}\n").Symbols[0].IsTest {
		t.Error("Test functions outside _test.go files are not tests")
	}
}

func TestBodyHashIgnoresWhitespace(t *testing.T) {
	a := extractSrc(t, "a.go", "package a\nfunc F() { return  1 }\n").Symbols[0].BodyHash
	b := extractSrc(t, "a.go", "package a\n\nfunc F() {\n\treturn 1\n}\n").Symbols[0].BodyHash
	c := extractSrc(t, "a.go", "package a\nfunc F() { return 2 }\n").Symbols[0].BodyHash
	if a != b || a == c || len(a) != 16 {
		t.Errorf("hashes %s %s %s", a, b, c)
	}
}

func TestInitSkippedAndTestNames(t *testing.T) {
	src := "package x\n\nfunc init() {}\nfunc init() {}\nfunc Test(t *T) {}\nfunc Testify() {}\n" +
		"func TestÜber() {}\nfunc Test_x() {}\nfunc Benchmarks() {}\nfunc ExampleFoo() {}\nfunc Fuzzy() {}\n" +
		"func (x) init() {}\n"
	res := extractSrc(t, "x_test.go", src)
	syms := byQualified(res)
	if _, ok := syms["init"]; ok {
		t.Error("func init() cannot be referenced and must be skipped")
	}
	if _, ok := syms["x.init"]; !ok {
		t.Error("a method named init is a normal method")
	}
	for name, want := range map[string]bool{
		"Test": true, "Testify": false, "TestÜber": true, "Test_x": true,
		"Benchmarks": false, "ExampleFoo": true, "Fuzzy": false,
	} {
		if syms[name].IsTest != want {
			t.Errorf("%s: IsTest %v, want %v", name, syms[name].IsTest, want)
		}
	}
}

func TestQualifiedNamesUnique(t *testing.T) {
	// Duplicate declarations only compile apart (build tags) or not at
	// all, but the store requires unique keys per file regardless.
	src := "package x\n\nfunc F() {}\nfunc F() {}\ntype T struct{ a int; a int }\nvar F = 1\n"
	seen := map[string]bool{}
	for _, s := range extractSrc(t, "x.go", src).Symbols {
		if seen[s.Qualified] {
			t.Errorf("duplicate qualified name %s", s.Qualified)
		}
		seen[s.Qualified] = true
	}
	if !seen["F~2"] || !seen["F~3"] || !seen["T.a~2"] {
		t.Errorf("got %v", seen)
	}
}

func TestBodyHashCoversSignatureNotDoc(t *testing.T) {
	a := extractSrc(t, "a.go", "package a\n// F one.\nfunc F(x int) int { return x }\n").Symbols[0].BodyHash
	b := extractSrc(t, "a.go", "package a\n// F two.\nfunc F(x int) int { return x }\n").Symbols[0].BodyHash
	c := extractSrc(t, "a.go", "package a\nfunc F(y int) int { return x }\n").Symbols[0].BodyHash
	if a != b || a == c {
		t.Errorf("hashes %s %s %s", a, b, c)
	}
}

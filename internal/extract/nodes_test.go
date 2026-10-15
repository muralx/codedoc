package extract_test

import (
	"testing"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/parse"
)

func TestCollapseAndBodyHash(t *testing.T) {
	if got := extract.Collapse("func F(\n\ta int,\n\tb int\n) error"); got != "func F(a int, b int) error" {
		t.Errorf("collapse: %q", got)
	}
	if extract.BodyHash("{ a b }") != extract.BodyHash("{\n\ta\tb\n}") || extract.BodyHash("ab") == extract.BodyHash("ac") {
		t.Error("body hash must ignore only whitespace")
	}
}

func TestSpansAndErrors(t *testing.T) {
	src := []byte("package p\n\nfunc F(\n\ta int,\n) {\n}\n")
	tree, err := parse.Parse("go", src)
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()
	fn := tree.Root().NamedChild(1)
	sig, text := extract.SignatureSpan(fn, fn.ChildByField("body").StartByte(), src)
	want := extract.Span{StartByte: 11, EndByte: 28, StartLine: 3, EndLine: 5}
	if sig != want || text != "func F(a int,)" {
		t.Errorf("signature %+v %q", sig, text)
	}
	if s := extract.SpanOf(fn); s.StartLine != 3 || s.EndLine != 6 || s.EndByte != uint(len(src)-1) {
		t.Errorf("span %+v", s)
	}
	if n := extract.CountErrors(tree.Root()); n != 0 {
		t.Errorf("clean file has %d errors", n)
	}
	bad, err := parse.Parse("go", []byte("package p\nfunc F( {\nvar x = \n"))
	if err != nil {
		t.Fatal(err)
	}
	defer bad.Close()
	if extract.CountErrors(bad.Root()) == 0 {
		t.Error("errors not counted")
	}
}

func TestLineEndAndNamedChildren(t *testing.T) {
	src := []byte("package p\n\nvar x = map[string]int{\n\t\"a\": 1,\n}\nvar y = 2\n")
	tree, err := parse.Parse("go", src)
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()
	vars := extract.NamedChildren(tree.Root(), "var_declaration")
	if len(vars) != 2 {
		t.Fatalf("got %d var declarations", len(vars))
	}
	if got := string(src[vars[0].StartByte():extract.LineEnd(vars[0], src)]); got != "var x = map[string]int{" {
		t.Errorf("first line %q", got)
	}
	if extract.LineEnd(vars[1], src) != vars[1].EndByte() {
		t.Error("single-line node ends at its end")
	}
}

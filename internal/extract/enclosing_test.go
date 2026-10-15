package extract_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/parse"
)

func TestEncloserPicksInnermost(t *testing.T) {
	e := extract.NewEncloser([]extract.Symbol{
		{Qualified: "F", Span: extract.Span{StartByte: 120, EndByte: 130}},
		{Qualified: "T.m", Span: extract.Span{StartByte: 10, EndByte: 50}},
		{Qualified: "T", Span: extract.Span{StartByte: 0, EndByte: 100}},
		{Qualified: "T.m.inner", Span: extract.Span{StartByte: 20, EndByte: 30}},
		{Qualified: "T.n", Span: extract.Span{StartByte: 60, EndByte: 70}},
		{Qualified: "T.n2", Span: extract.Span{StartByte: 60, EndByte: 70}},
	})
	for off, want := range map[uint]string{
		0: "T", 10: "T.m", 25: "T.m.inner", 30: "T.m", 49: "T.m", 50: "T", 65: "T.n", 75: "T",
		110: "", 129: "F", 130: "", 200: "",
	} {
		if got := e.At(off); got != want {
			t.Errorf("At(%d) = %q, want %q", off, got, want)
		}
	}
}

func TestEncloserEmpty(t *testing.T) {
	if got := extract.NewEncloser(nil).At(5); got != "" {
		t.Errorf("At on empty = %q", got)
	}
}

// BenchmarkEncloser40k looks up the enclosing symbol of 160k references
// among 40k functions; the whole run must stay far below a second.
func BenchmarkEncloser40k(b *testing.B) {
	const funcs, refsPer = 40000, 4
	syms := make([]extract.Symbol, funcs)
	for i := range syms {
		start := uint(i * 100)
		syms[i] = extract.Symbol{Qualified: fmt.Sprintf("f%d", i), Span: extract.Span{StartByte: start, EndByte: start + 90}}
	}
	b.ResetTimer()
	for b.Loop() {
		e := extract.NewEncloser(syms)
		for i := 0; i < funcs*refsPer; i++ {
			if e.At(uint(i*25)) == "x" {
				b.Fatal("impossible")
			}
		}
	}
}

func TestEncloser40kIsFast(t *testing.T) {
	r := testing.Benchmark(BenchmarkEncloser40k)
	if r.N > 0 && r.NsPerOp() > int64(time.Second) {
		t.Errorf("40k functions / 160k refs took %v", time.Duration(r.NsPerOp()))
	}
}

func TestIsFieldAndArgCount(t *testing.T) {
	src := []byte("package p\nvar x = f(a, /* c */ b)\n")
	tree, err := parse.Parse("go", src)
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()
	call := findKind(tree.Root(), "call_expression")
	fn, args := call.ChildByField("function"), call.ChildByField("arguments")
	if !extract.IsField(call, "function", fn) || extract.IsField(call, "arguments", fn) || extract.IsField(nil, "x", fn) {
		t.Error("IsField")
	}
	if extract.ArgCount(args) != 2 || extract.ArgCount(nil) != 0 {
		t.Errorf("ArgCount %d", extract.ArgCount(args))
	}
	if extract.Line(call) != 2 {
		t.Errorf("Line %d", extract.Line(call))
	}
}

func findKind(n parse.Node, kind string) parse.Node {
	if n.Kind() == kind {
		return n
	}
	for i := uint(0); i < n.NamedChildCount(); i++ {
		if f := findKind(n.NamedChild(i), kind); f != nil {
			return f
		}
	}
	return nil
}

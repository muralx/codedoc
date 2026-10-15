package python_test

import (
	"testing"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/extract/golden"
	"github.com/muralx/codedoc/internal/extract/python"
)

func TestGolden(t *testing.T) {
	golden.RunFixtures(t, python.New(), golden.Sources(t, "py-", ".py"))
}

func TestRegistered(t *testing.T) {
	if _, ok := extract.For("python"); !ok {
		t.Fatal("python extractor not registered")
	}
}

func extractSrc(t *testing.T, path, src string) *extract.FileResult {
	t.Helper()
	res, err := python.New().Extract(path, []byte(src))
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

const edgeCases = `# A leading comment.
r"""Module doc.

	Tabbed line.
"""

X = 1
X = 2
_y: int = 3
A, B = 1, 2
x.attr = 4


@dec
@other(1)
def decorated(a,
              b) -> int:
    # comment before the docstring
    """Doc
       spans lines.
    """
    return a


class Outer:
    f"""Not a docstring."""
    count = 0

    class Inner:
        def m(self):
            pass

    def __init__(this, v):
        this.count = v
        this.__secret = 1
        if v:
            this.late = 2
        def local():
            this.ignored = 3

    def __private(self):
        pass

    def _package(self):
        pass

    def __dunder__(self):
        pass

    @property
    def p(self):
        return 1

    @p.setter
    def p(self, v):
        pass


def outer():
    def nested():
        pass
    class Local:
        pass
`

func TestEdgeCases(t *testing.T) {
	res := extractSrc(t, "pkg/mod.py", edgeCases)
	syms := byQualified(res)
	want := []string{
		"X", "_y", "decorated", "Outer", "Outer.count", "Outer.Inner", "Outer.Inner.m",
		"Outer.__init__", "Outer.__secret", "Outer.late", "Outer.__private", "Outer._package",
		"Outer.__dunder__", "Outer.p", "Outer.p~2", "outer",
	}
	if len(res.Symbols) != len(want) {
		for _, s := range res.Symbols {
			t.Logf("%s %s", s.Kind, s.Qualified)
		}
		t.Fatalf("got %d symbols, want %d", len(res.Symbols), len(want))
	}
	for i, q := range want {
		if res.Symbols[i].Qualified != q {
			t.Errorf("symbol %d: got %s want %s", i, res.Symbols[i].Qualified, q)
		}
	}
	if res.ModuleDoc == nil || res.ModuleDoc.Text != "Module doc.\n\nTabbed line." {
		t.Errorf("module doc %+v", res.ModuleDoc)
	}
	checkKinds(t, syms)
	checkVisibility(t, syms)
	checkDecorated(t, syms["decorated"])
	if syms["Outer"].Doc != nil {
		t.Error("an f-string is not a docstring")
	}
	if s := syms["Outer.Inner"]; s.Container != "Outer" {
		t.Errorf("Inner container %q", s.Container)
	}
	if s := syms["Outer.late"]; s.Container != "Outer" || s.SigText != "this.late = 2" || s.Span.StartLine != 37 {
		t.Errorf("late %+v", s)
	}
}

func checkKinds(t *testing.T, syms map[string]extract.Symbol) {
	t.Helper()
	for q, k := range map[string]extract.Kind{
		"X": extract.KindConst, "_y": extract.KindVar, "decorated": extract.KindFunction,
		"Outer": extract.KindClass, "Outer.count": extract.KindField, "Outer.Inner.m": extract.KindMethod,
		"Outer.__secret": extract.KindField,
	} {
		if syms[q].Kind != k {
			t.Errorf("%s: kind %s want %s", q, syms[q].Kind, k)
		}
	}
	if syms["X"].SigText != "X = 1" {
		t.Errorf("X keeps its first assignment, got %q", syms["X"].SigText)
	}
}

func checkVisibility(t *testing.T, syms map[string]extract.Symbol) {
	t.Helper()
	for q, v := range map[string]extract.Visibility{
		"X": extract.Public, "_y": extract.Package, "Outer.__private": extract.Private,
		"Outer.__secret": extract.Private, "Outer._package": extract.Package,
		"Outer.__dunder__": extract.Public, "Outer.__init__": extract.Public,
	} {
		if syms[q].Visibility != v {
			t.Errorf("%s: visibility %s want %s", q, syms[q].Visibility, v)
		}
	}
}

func checkDecorated(t *testing.T, s extract.Symbol) {
	t.Helper()
	if s.Span.StartLine != 14 || s.Span.EndLine != 22 {
		t.Errorf("decorated span %+v", s.Span)
	}
	if s.Signature.StartLine != 16 || s.Signature.EndLine != 17 || s.SigText != "def decorated(a, b) -> int" {
		t.Errorf("decorated signature %+v %q", s.Signature, s.SigText)
	}
	if s.Doc == nil || s.Doc.Text != "Doc\nspans lines." || s.Doc.Span.StartLine != 19 {
		t.Errorf("decorated doc %+v", s.Doc)
	}
}

func TestBodyHashIgnoresDocstring(t *testing.T) {
	a := extractSrc(t, "m.py", "def f():\n    \"\"\"One.\"\"\"\n    return 1\n").Symbols[0]
	b := extractSrc(t, "m.py", "def f():\n    '''Two\n    lines.'''\n    return  1\n").Symbols[0]
	c := extractSrc(t, "m.py", "def f():\n    return 2\n").Symbols[0]
	if a.BodyHash != b.BodyHash {
		t.Error("editing the docstring changed the body hash")
	}
	if a.BodyHash == c.BodyHash {
		t.Error("editing the body kept the body hash")
	}
	if b.Doc.Text != "Two\nlines." {
		t.Errorf("doc %q", b.Doc.Text)
	}
}

func TestTests(t *testing.T) {
	src := "def test_a():\n    pass\n\ndef helper():\n    pass\n\nclass TestX:\n    def test_m(self):\n        pass\n" +
		"    def setup(self):\n        pass\n\nclass Other:\n    def test_n(self):\n        pass\n"
	wantTests := map[string]bool{"test_a": true, "TestX.test_m": true}
	for _, path := range []string{"test_mod.py", "pkg/mod_test.py", "tests/mod.py", "a/tests/sub/mod.py"} {
		for _, s := range extractSrc(t, path, src).Symbols {
			if s.IsTest != wantTests[s.Qualified] {
				t.Errorf("%s %s: IsTest %v", path, s.Qualified, s.IsTest)
			}
		}
	}
	for _, path := range []string{"mod.py", "testing/mod.py", "contest.py"} {
		for _, s := range extractSrc(t, path, src).Symbols {
			if s.IsTest {
				t.Errorf("%s %s: a test outside a test file", path, s.Qualified)
			}
		}
	}
}

func TestParseErrors(t *testing.T) {
	if res := extractSrc(t, "m.py", "def f(:\n"); res.ParseErrors == 0 {
		t.Error("parse errors not counted")
	}
}

func TestBodyHashCoversSignature(t *testing.T) {
	a := extractSrc(t, "m.py", "def f(x):\n    \"\"\"One.\"\"\"\n    return 1\n").Symbols[0]
	b := extractSrc(t, "m.py", "def f(y):\n    \"\"\"One.\"\"\"\n    return 1\n").Symbols[0]
	if a.BodyHash == b.BodyHash {
		t.Error("renaming a parameter kept the body hash")
	}
}

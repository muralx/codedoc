package extract_test

import (
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/parse"
)

var goChains = extract.ChainGrammar{
	Names:     map[string]bool{"identifier": true, "field_identifier": true},
	Selectors: map[string][2]string{"selector_expression": {"operand", "field"}},
	Calls:     map[string]string{"call_expression": "function"},
}

// qualifierOf renders the qualifier text of a Go expression.
func qualifierOf(t *testing.T, expr string) string {
	t.Helper()
	src := []byte("package p\nvar _ = " + expr + "\n")
	tree, err := parse.Parse("go", src)
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()
	return goChains.Qualifier(findKind(tree.Root(), "expression_list").NamedChild(0), src)
}

func TestQualifierChains(t *testing.T) {
	for expr, want := range map[string]string{
		"a.b.c":           "a.b.c",
		"x.Do(1, 2).Then": "x.Do().Then",
		"f()":             "f()",
		"func() {}()":     "()",
		"xs[0].f":         "().f",
	} {
		if got := qualifierOf(t, expr); got != want {
			t.Errorf("%s: got %q, want %q", expr, got, want)
		}
	}
	got := qualifierOf(t, strings.Repeat("seg.", 40)+"end")
	if len(got) > extract.MaxQualifier || !strings.HasPrefix(got, "seg.") || !strings.HasSuffix(got, ".end") {
		t.Errorf("long chain: %q", got)
	}
	if got := goChains.Qualifier(nil, nil); got != "()" {
		t.Errorf("nil node: %q", got)
	}
}

var javaChains = extract.ChainGrammar{
	Names:       map[string]bool{"identifier": true, "this": true},
	Selectors:   map[string][2]string{"field_access": {"object", "field"}},
	Invocations: map[string][2]string{"method_invocation": {"object", "name"}},
}

func TestQualifierInvocations(t *testing.T) {
	for expr, want := range map[string]string{
		"a.b().c":         "a.b().c",
		"this.f.get(1).x": "this.f.get().x",
		"g().x":           "g().x",
		"(a).b().x":       "().b().x",
	} {
		src := []byte("class A { Object o = " + expr + "; }\n")
		tree, err := parse.Parse("java", src)
		if err != nil {
			t.Fatal(err)
		}
		n := findKind(tree.Root(), "variable_declarator").ChildByField("value")
		if got := javaChains.Qualifier(n, src); got != want {
			t.Errorf("%s: got %q, want %q", expr, got, want)
		}
		tree.Close()
	}
}

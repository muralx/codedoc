package extract_test

import (
	"testing"

	"github.com/muralx/codedoc/internal/extract"
	_ "github.com/muralx/codedoc/internal/extract/golang"
	_ "github.com/muralx/codedoc/internal/extract/java"
	_ "github.com/muralx/codedoc/internal/extract/python"
	_ "github.com/muralx/codedoc/internal/extract/ts"
	"github.com/muralx/codedoc/internal/parse"
)

// hashCase is one function in three versions: the original, the same
// code reformatted, and the code with whitespace inside a literal
// removed.
type hashCase struct {
	lang, path, name           string
	orig, reformatted, literal string
}

var hashCases = []hashCase{
	{parse.Go, "a.go", "F",
		"package p\n\nfunc F() string { return \"a b\" + `c d` + string(' ') }\n",
		"package p\n\nfunc F() string {\n\treturn \"a b\" +\n\t\t`c d` + string( ' ' )\n}\n",
		"package p\n\nfunc F() string { return \"ab\" + `c d` + string(' ') }\n"},
	{parse.Java, "A.java", "A.f",
		"class A {\n  String f() { return \"a b\" + ' '; }\n}\n",
		"class A {\n  String f() {\n    return \"a b\"\n      + ' ';\n  }\n}\n",
		"class A {\n  String f() { return \"ab\" + ' '; }\n}\n"},
	{parse.Python, "m.py", "f",
		"def f():\n    return 'a b' + f\"x {y} z\"\n",
		"def f():\n    return   'a b'+f\"x { y } z\"\n",
		"def f():\n    return 'a b' + f\"x {y}z\"\n"},
	{parse.TypeScript, "a.ts", "f",
		"export function f() { return 'a b' + `c ${d} e`; }\n",
		"export function f() {\n  return 'a b' +\n    `c ${ d } e`;\n}\n",
		"export function f() { return 'ab' + `c ${d} e`; }\n"},
}

func literalHash(t *testing.T, c hashCase, src string) string {
	t.Helper()
	ex, ok := extract.For(c.lang)
	if !ok {
		t.Fatalf("no extractor for %s", c.lang)
	}
	res, err := ex.Extract(c.path, []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range res.Symbols {
		if s.Qualified == c.name || s.Name == c.name {
			return s.BodyHash
		}
	}
	t.Fatalf("%s: no symbol %s in %+v", c.lang, c.name, res.Symbols)
	return ""
}

func TestBodyHashKeepsWhitespaceInsideLiterals(t *testing.T) {
	for _, c := range hashCases {
		orig := literalHash(t, c, c.orig)
		if got := literalHash(t, c, c.reformatted); got != orig {
			t.Errorf("%s: reformatting changed the body hash", c.lang)
		}
		if got := literalHash(t, c, c.literal); got == orig {
			t.Errorf("%s: removing a space inside a literal kept the body hash", c.lang)
		}
	}
}

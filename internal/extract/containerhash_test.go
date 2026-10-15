package extract_test

import (
	"testing"

	"github.com/muralx/codedoc/internal/extract"
	_ "github.com/muralx/codedoc/internal/extract/golang"
	_ "github.com/muralx/codedoc/internal/extract/java"
	_ "github.com/muralx/codedoc/internal/extract/python"
	_ "github.com/muralx/codedoc/internal/extract/ts"
)

// containerCase holds one container in four versions: as is, with a
// member doc added, with a member body edited, and with a member added.
type containerCase struct {
	lang, path, qualified        string
	base, doc, body, added, more string
}

var containerCases = []containerCase{
	{lang: "go", path: "p.go", qualified: "T",
		base:  "package p\n\ntype T struct {\n\tA int\n}\n\ntype I interface {\n\tM() int\n}\n",
		doc:   "package p\n\ntype T struct {\n\t// A is documented.\n\tA int\n}\n\ntype I interface {\n\tM() int\n}\n",
		body:  "package p\n\ntype T struct {\n\tA int // trailing\n}\n\ntype I interface {\n\tM() int\n}\n",
		added: "package p\n\ntype T struct {\n\tA int\n\tB string\n}\n\ntype I interface {\n\tM() int\n}\n",
		more:  "package p\n\ntype T struct {\n\tA int\n}\n\ntype I interface {\n\tM() int\n\tio.Reader\n}\n"},
	{lang: "typescript", path: "a.ts", qualified: "a.ts/C",
		base:  "export class C {\n  m(): number {\n    return 1;\n  }\n}\n",
		doc:   "export class C {\n  /** M is documented. */\n  m(): number {\n    return 1;\n  }\n}\n",
		body:  "export class C {\n  m(): number {\n    return 2;\n  }\n}\n",
		added: "export class C {\n  m(): number {\n    return 1;\n  }\n  n(): void {}\n}\n"},
	{lang: "python", path: "a.py", qualified: "C",
		base:  "class C:\n    def m(self):\n        return 1\n",
		doc:   "class C:\n    def m(self):\n        \"\"\"M is documented.\"\"\"\n        return 1\n",
		body:  "class C:\n    def m(self):\n        return 2\n",
		added: "class C:\n    def m(self):\n        return 1\n\n    def n(self):\n        pass\n"},
	{lang: "java", path: "C.java", qualified: "C",
		base:  "class C {\n  int m() {\n    return 1;\n  }\n}\n",
		doc:   "class C {\n  /** M is documented. */\n  int m() {\n    return 1;\n  }\n}\n",
		body:  "class C {\n  int m() {\n    return 2;\n  }\n}\n",
		added: "class C {\n  int f;\n  int m() {\n    return 1;\n  }\n}\n"},
}

func hashOf(t *testing.T, lang, path, qualified, src string) string {
	t.Helper()
	ex, ok := extract.For(lang)
	if !ok {
		t.Fatalf("no extractor for %s", lang)
	}
	res, err := ex.Extract(path, []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range res.Symbols {
		if s.Qualified == qualified {
			return s.BodyHash
		}
	}
	t.Fatalf("%s not found in\n%s", qualified, src)
	return ""
}

func TestContainerHashIgnoresMemberDocsAndBodies(t *testing.T) {
	for _, c := range containerCases {
		t.Run(c.lang, func(t *testing.T) {
			base := hashOf(t, c.lang, c.path, c.qualified, c.base)
			if got := hashOf(t, c.lang, c.path, c.qualified, c.doc); got != base {
				t.Error("a member doc moved the container hash")
			}
			if got := hashOf(t, c.lang, c.path, c.qualified, c.body); got != base {
				t.Error("a member body edit moved the container hash")
			}
			if got := hashOf(t, c.lang, c.path, c.qualified, c.added); got == base {
				t.Error("an added member left the container hash")
			}
			if c.more != "" && hashOf(t, c.lang, c.path, "I", c.more) == hashOf(t, c.lang, c.path, "I", c.base) {
				t.Error("an embedded interface left the interface hash")
			}
		})
	}
}

func TestTSEnumHashCoversMembers(t *testing.T) {
	a := hashOf(t, "typescript", "e.ts", "e.ts/E", "export enum E {\n  A,\n}\n")
	b := hashOf(t, "typescript", "e.ts", "e.ts/E", "export enum E {\n  /** A doc. */\n  A,\n}\n")
	c := hashOf(t, "typescript", "e.ts", "e.ts/E", "export enum E {\n  A,\n  B,\n}\n")
	if a != b || a == c {
		t.Fatalf("enum hash: doc moved it %v, member left it %v", a != b, a == c)
	}
}

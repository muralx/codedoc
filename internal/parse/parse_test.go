package parse_test

import (
	"testing"

	"github.com/muralx/codedoc/internal/parse"
)

func TestParseEveryLanguage(t *testing.T) {
	for _, tc := range []struct{ lang, src, rootKind string }{
		{"go", "package p\nfunc F() {}\n", "source_file"},
		{"typescript", "export function f() {}\n", "program"},
		{"tsx", "const x = <div/>;\n", "program"},
		{"javascript", "const x = <div/>;\n", "program"},
		{"python", "def f():\n    pass\n", "module"},
		{"java", "class A { void f() {} }\n", "program"},
		{"bash", "export X=1\necho \"$X\"\n", "program"},
	} {
		tree, err := parse.Parse(tc.lang, []byte(tc.src))
		if err != nil {
			t.Fatalf("%s: %v", tc.lang, err)
		}
		if k := tree.Root().Kind(); k != tc.rootKind {
			t.Fatalf("%s: root %s", tc.lang, k)
		}
		if tree.Root().HasError() {
			t.Fatalf("%s: unexpected parse error", tc.lang)
		}
		tree.Close()
	}
	if _, err := parse.Parse("cobol", []byte("x")); err == nil {
		t.Fatal("unknown language must error")
	}
}

func TestLanguageForPath(t *testing.T) {
	for p, want := range map[string]string{
		"a.go": "go", "b.ts": "typescript", "c.tsx": "tsx", "d.js": "javascript",
		"e.jsx": "javascript", "f.mjs": "javascript", "g.cjs": "javascript", "dir/h.d.ts": "typescript",
		"i.mts": "typescript", "j.CTS": "typescript", "k.py": "python", "l/M.java": "java",
		"e.txt": "", "Makefile": "", "run.sh": "",
	} {
		if got := parse.LanguageForPath(p); got != want {
			t.Fatalf("%s: %q", p, got)
		}
	}
}

func TestFamily(t *testing.T) {
	for lang, want := range map[string]string{
		"go": "go", "typescript": "ts", "tsx": "ts", "javascript": "ts", "python": "py", "java": "java", "bash": "", "cobol": "",
	} {
		if got := parse.Family(lang); got != want {
			t.Fatalf("%s: %q", lang, got)
		}
	}
}

func TestNodeNavigation(t *testing.T) {
	src := []byte("package p\n\n// F does it.\nfunc F(a int) {}\n")
	tree, err := parse.Parse("go", src)
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()
	root := tree.Root()
	fn := root.NamedChild(root.NamedChildCount() - 1)
	name := fn.ChildByField("name")
	for _, c := range []struct {
		what      string
		got, want any
	}{
		{"root parent", root.Parent(), nil},
		{"root prev", root.PrevSibling(), nil},
		{"root next", root.NextSibling(), nil},
		{"kind", fn.Kind(), "function_declaration"},
		{"named", fn.IsNamed(), true},
		{"error", fn.HasError(), false},
		{"rows", [2]uint{fn.StartRow(), fn.EndRow()}, [2]uint{3, 3}},
		{"bytes", [2]uint{fn.StartByte(), fn.EndByte()}, [2]uint{25, uint(len(src) - 1)}},
		{"name text", name.Text(src), "F"},
		{"name parent", name.Parent().Kind(), "function_declaration"},
		{"absent field", fn.ChildByField("result"), nil},
		{"prev sibling", fn.PrevSibling().Kind(), "comment"},
		{"next sibling", fn.PrevSibling().NextSibling().Kind(), "function_declaration"},
		{"anonymous child", fn.Child(0).Kind(), "func"},
		{"anonymous child named", fn.Child(0).IsNamed(), false},
		{"more children than named", fn.ChildCount() > fn.NamedChildCount(), true},
		{"child out of range", fn.Child(99), nil},
		{"named child out of range", fn.NamedChild(99), nil},
	} {
		if c.got != c.want {
			t.Errorf("%s: got %v want %v", c.what, c.got, c.want)
		}
	}
}

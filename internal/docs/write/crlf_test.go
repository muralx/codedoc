package write

import (
	"strings"
	"testing"
)

// crlf converts LF line ends to CRLF.
func crlf(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }

func TestCRLFFilesStayCRLF(t *testing.T) {
	cases := []struct {
		name, path, lang, qualified, src string
	}{
		{"go", "a.go", "go", "F", "package a\n\nfunc F() {}\n\n// G is old.\nfunc G() {}\n"},
		{"go replace", "a.go", "go", "G", "package a\n\nfunc F() {}\n\n// G is old.\nfunc G() {}\n"},
		{"ts", "a.ts", "typescript", "a.ts/f", "export function f(): void {}\n\n/** Old. */\nexport function g(): void {}\n"},
		{"ts replace", "a.ts", "typescript", "a.ts/g", "export function f(): void {}\n\n/** Old. */\nexport function g(): void {}\n"},
		{"python", "m.py", "python", "f", "def f():\n    return 1\n\n\ndef g(): return 2\n"},
		{"python inline", "m.py", "python", "g", "def f():\n    return 1\n\n\ndef g(): return 2\n"},
		{"java", "A.java", "java", "A.f", "class A {\n  /** Old. */\n  void f() {}\n}\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := repo(t, map[string]string{c.path: crlf(c.src)})
			tg := Target{Root: root, Path: c.path, Lang: c.lang, Qualified: c.qualified}
			for _, text := range []string{"Does a lot of things.", "Does a lot of things.\n\nAnd more."} {
				res, err := Apply(tg, text)
				if err != nil {
					t.Fatal(err)
				}
				got := read(t, root, c.path)
				if strings.Count(got, "\n") != strings.Count(got, "\r\n") {
					t.Fatalf("mixed line ends:\n%q", got)
				}
				if !strings.Contains(normalize(res.Doc.Text), "a lot of things.") || strings.Contains(res.Doc.Text, "\r") {
					t.Fatalf("doc %q", res.Doc.Text)
				}
			}
		})
	}
}

func TestCRLFModuleDocs(t *testing.T) {
	files := map[string]string{"p/doc.go": crlf("// Package p is old.\npackage p\n"),
		"q/README.md":   crlf("# Q\n\n## Purpose\n\nOld.\n\n## Usage\n\nRun it.\n"),
		"r/__init__.py": crlf("import os\n"), "s/package-info.java": crlf("package s;\n")}
	root := repo(t, files)
	for _, tg := range []Target{{Path: "p", Lang: "go", ModuleDir: "p"}, {Path: "q", Lang: "typescript", ModuleDir: "q"},
		{Path: "r", Lang: "python", ModuleDir: "r"}, {Path: "s", Lang: "java", ModuleDir: "s"}} {
		tg.Root = root
		res, err := Apply(tg, "Does module things.")
		if err != nil {
			t.Fatalf("%s: %v", tg.Path, err)
		}
		got := read(t, root, res.Path)
		if strings.Count(got, "\n") != strings.Count(got, "\r\n") {
			t.Fatalf("%s: mixed line ends:\n%q", res.Path, got)
		}
	}
}

func TestPythonCRLFLineInLFFile(t *testing.T) {
	src := "def f():\r\n    return 1\n\n\ndef g():\n    pass\n\n\ndef h():\n    pass\n"
	root := repo(t, map[string]string{"m.py": src})
	if _, err := Apply(Target{Root: root, Path: "m.py", Lang: "python", Qualified: "f"}, "F."); err != nil {
		t.Fatal(err)
	}
	if got := read(t, root, "m.py"); got != "def f():\r\n    \"\"\"F.\"\"\"\n    return 1\n"+src[len("def f():\r\n    return 1\n"):] {
		t.Fatalf("got %q", got)
	}
}

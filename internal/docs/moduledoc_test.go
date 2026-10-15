package docs_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/muralx/codedoc/internal/docs"
	"github.com/muralx/codedoc/internal/extract"
	_ "github.com/muralx/codedoc/internal/extract/golang" // Go extractor
	_ "github.com/muralx/codedoc/internal/extract/java"   // Java extractor
	_ "github.com/muralx/codedoc/internal/extract/python" // Python extractor
	_ "github.com/muralx/codedoc/internal/extract/ts"     // TS extractor
)

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestREADMEModuleDoc(t *testing.T) {
	for _, tc := range []struct{ readme, want string }{
		{"Plain text only.\n", "Plain text only."},
		{"# Title\n\nUnder the title.\n\n## More\n\nlater\n", "Under the title."},
		{"# Title\n\nIntro.\n\n```\n# not a heading\n```\n\n## Purpose\n\nThe purpose.\n\n## Next\n", "The purpose."},
		{"# Title only\n", ""},
		{"## Purpose\n\nThe purpose.\n\n### Detail\n\nMore.\n\n## Usage\n\nRun.\n", "The purpose.\n\n### Detail\n\nMore."},
	} {
		root := t.TempDir()
		write(t, root, "pkg/README.md", tc.readme)
		d, err := docs.Lookup{Root: root}.Find(docs.ModuleRef{Path: "pkg", Lang: "ts", Kind: "dir"}, nil)
		if err != nil || d.Text != tc.want {
			t.Errorf("%q: got %+v %v, want %q", tc.readme, d, err, tc.want)
		}
	}
}

func TestGoModuleDocOrderAndIncrementalRule(t *testing.T) {
	root := t.TempDir()
	write(t, root, "p/b.go", "// Package p from b.\npackage p\n")
	write(t, root, "p/c.go", "// Package p from c.\npackage p\n")
	write(t, root, "p/a_test.go", "// Package p from a test.\npackage p\n")
	look := docs.Lookup{Root: root}
	ref := docs.ModuleRef{Path: "p", Lang: "go", Kind: "go-package"}
	if d, err := look.Find(ref, nil); err != nil || d.Source != "p/b.go" || d.State() != docs.OK {
		t.Fatalf("full lookup %+v %v", d, err)
	}
	cur := &docs.ModuleDoc{Text: "Package p from b.", Source: "p/b.go"}
	look.Known = map[string]*extract.Doc{"p/doc.go": {Text: "Package p from doc."}, "p/c.go": nil}
	if d, _ := look.Find(ref, cur); d.Source != "p/doc.go" {
		t.Fatalf("doc.go outranks %+v", d)
	}
	look.Known = map[string]*extract.Doc{"p/b.go": nil}
	if d, _ := look.Find(ref, cur); d.Source != "p/c.go" {
		t.Fatalf("a touched source that lost its doc falls back to the next file: %+v", d)
	}
	if d, _ := look.Find(docs.ModuleRef{Path: "x", Lang: "rust"}, nil); d.State() != docs.Missing {
		t.Fatalf("unknown language %+v", d)
	}
}

func TestModuleDocOtherLanguages(t *testing.T) {
	root := t.TempDir()
	write(t, root, "pkg/__init__.py", "\"\"\"Package doc.\"\"\"\n")
	write(t, root, "pkg/mod.py", "\"\"\"Module doc.\"\"\"\nx = 1\n")
	write(t, root, "j/package-info.java", "/** Java package. */\npackage j;\n")
	write(t, root, "t/index.ts", "/** Index doc. */\n\nexport const x = 1;\n")
	look := docs.Lookup{Root: root}
	for _, tc := range []struct {
		ref        docs.ModuleRef
		text, from string
	}{
		{docs.ModuleRef{Path: "pkg", Lang: "py", Kind: "py-package"}, "Package doc.", "pkg/__init__.py"},
		{docs.ModuleRef{Path: "pkg/mod.py", Lang: "py", Kind: "py-module"}, "Module doc.", "pkg/mod.py"},
		{docs.ModuleRef{Path: "j", Lang: "java", Kind: "java-package"}, "Java package.", "j/package-info.java"},
		{docs.ModuleRef{Path: "t", Lang: "ts", Kind: "dir"}, "Index doc.", "t/index.ts"},
		{docs.ModuleRef{Path: "gone", Lang: "ts", Kind: "dir"}, "", ""},
		{docs.ModuleRef{Path: "gone", Lang: "java", Kind: "java-package"}, "", ""},
	} {
		d, err := look.Find(tc.ref, nil)
		if err != nil || d.Text != tc.text || d.Source != tc.from {
			t.Errorf("%+v: got %+v %v", tc.ref, d, err)
		}
	}
}

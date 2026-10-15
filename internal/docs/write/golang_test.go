package write

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestGoSymbolDocs(t *testing.T) {
	src := "package p\n\nfunc Plain() {}\n\n// Old says\n// things.\n//\n//go:noinline\nfunc Old() {}\n\ntype T struct {\n\t// N is old.\n\tN int\n}\n\nvar (\n\tA = 1\n)\n"
	files := map[string]string{"p/p.go": src}
	tg := func(q string) Target { return Target{Path: "p/p.go", Lang: "go", Qualified: q} }
	long := "Returns the configured value after reading every configuration source in order of precedence."
	runRoundTrips(t, []roundTrip{
		{name: "create", files: files, tg: tg("Plain"), text: long, file: "p/p.go",
			want: "package p\n\n// Plain returns the configured value after reading every configuration source\n// in order of precedence.\nfunc Plain() {}\n" + src[len("package p\n\nfunc Plain() {}\n"):],
			doc:  "Plain returns the configured value after reading every configuration source\nin order of precedence."},
		{name: "replace keeps directives", files: files, tg: tg("Old"), text: "Old is new.\n\nSecond paragraph.  \n", file: "p/p.go",
			want: "package p\n\nfunc Plain() {}\n\n// Old is new.\n//\n// Second paragraph.\n//\n//go:noinline\nfunc Old() {}\n" + src[len("package p\n\nfunc Plain() {}\n\n// Old says\n// things.\n//\n//go:noinline\nfunc Old() {}\n"):],
			doc:  "Old is new.\n\nSecond paragraph."},
		{name: "indented field", files: files, tg: tg("T.N"), text: "N counts.", file: "p/p.go",
			want: "package p\n\nfunc Plain() {}\n\n// Old says\n// things.\n//\n//go:noinline\nfunc Old() {}\n\ntype T struct {\n\t// N counts.\n\tN int\n}\n\nvar (\n\tA = 1\n)\n",
			doc:  "N counts."},
		{name: "grouped var", files: files, tg: tg("A"), text: "HTTP port.", file: "p/p.go",
			want: src[:len(src)-len("\tA = 1\n)\n")] + "\t// A HTTP port.\n\tA = 1\n)\n",
			doc:  "A HTTP port."},
	})
}

func TestGoDocGoesAboveDirectives(t *testing.T) {
	src := "package p\n\n//go:noinline\n//go:nosplit\nfunc Hot() {}\n"
	runRoundTrips(t, []roundTrip{
		{name: "insert", files: map[string]string{"p.go": src}, tg: Target{Path: "p.go", Lang: "go", Qualified: "Hot"},
			text: "Runs hot.", file: "p.go",
			want: "package p\n\n// Hot runs hot.\n//\n//go:noinline\n//go:nosplit\nfunc Hot() {}\n", doc: "Hot runs hot."},
	})
}

func TestGoModuleDocs(t *testing.T) {
	runRoundTrips(t, []roundTrip{
		{name: "create doc.go", files: map[string]string{"s/a.go": "package store\n", "s/a_test.go": "package store_test\n"},
			tg: Target{Path: "s", Lang: "go", ModuleDir: "s"}, text: "Implements storage.", file: "s/doc.go",
			want: "// Package store implements storage.\npackage store\n", doc: "Package store implements storage."},
		{name: "test files only", files: map[string]string{"s/a_test.go": "package store_test\n"},
			tg: Target{Path: "s", Lang: "go", ModuleDir: "s"}, text: "Package store tests.", file: "s/doc.go",
			want: "// Package store tests.\npackage store\n", doc: "Package store tests."},
		{name: "replace in doc.go", files: map[string]string{"doc.go": "// Package x is old.\n//\n// More.\npackage x\n"},
			tg: Target{Path: ".", Lang: "go", ModuleDir: "."}, text: "Package x is new.", file: "doc.go",
			want: "// Package x is new.\npackage x\n", doc: "Package x is new."},
		{name: "insert in doc.go", files: map[string]string{"x/doc.go": "//go:build linux\n\npackage x\n"},
			tg: Target{Path: "x", Lang: "go", ModuleDir: "x"}, text: "Does x.", file: "x/doc.go",
			want: "//go:build linux\n\n// Package x does x.\npackage x\n", doc: "Package x does x."},
	})
}

func TestGoModuleDocReplacesTheCommentOutsideDocGo(t *testing.T) {
	files := map[string]string{"s/b.go": "// Package store is old.\npackage store\n", "s/a.go": "package store\n",
		"s/c.go": "// Package store, again.\npackage store\n", "s/a_test.go": "// Package store tests.\npackage store\n"}
	runRoundTrips(t, []roundTrip{{name: "first commented file", files: files,
		tg: Target{Path: "s", Lang: "go", ModuleDir: "s"}, text: "Is new.", file: "s/b.go",
		want: "// Package store is new.\npackage store\n", doc: "Package store is new."}})
	root := repo(t, files)
	res, err := Apply(Target{Root: root, Path: "s", Lang: "go", ModuleDir: "s"}, "Is new.")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "s", "doc.go")); !os.IsNotExist(err) {
		t.Fatalf("doc.go created beside an existing package comment: %v", err)
	}
	if !slices.Equal(res.Others, []string{"s/c.go"}) {
		t.Fatalf("others %v", res.Others)
	}
}

func TestGoModuleDocKeepsDocGoAndReportsOthers(t *testing.T) {
	root := repo(t, map[string]string{"s/doc.go": "package store\n", "s/a.go": "// Package store is elsewhere.\npackage store\n"})
	res, err := Apply(Target{Root: root, Path: "s", Lang: "go", ModuleDir: "s"}, "Is here.")
	if err != nil {
		t.Fatal(err)
	}
	if res.Path != "s/doc.go" || read(t, root, "s/doc.go") != "// Package store is here.\npackage store\n" ||
		read(t, root, "s/a.go") != "// Package store is elsewhere.\npackage store\n" {
		t.Fatalf("result %+v", res)
	}
	if !slices.Equal(res.Others, []string{"s/a.go"}) {
		t.Fatalf("others %v", res.Others)
	}
}

func TestGoModuleDocErrors(t *testing.T) {
	root := repo(t, map[string]string{"e/readme.txt": "", "n/doc.go": "// just a comment\n"})
	if _, err := Plan(Target{Root: root, Path: "e", Lang: "go", ModuleDir: "e"}, "x"); err == nil {
		t.Fatal("directory without go files accepted")
	}
	if _, err := Plan(Target{Root: root, Path: "n", Lang: "go", ModuleDir: "n"}, "x"); err == nil {
		t.Fatal("doc.go without package clause accepted")
	}
}

package query_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/query"
)

// sitesWorkspace has a function calling another three times, on the
// lines 6, 7 and 10 of m.go inside Use (5-12), and a function referring
// to a type on two lines (17 and 18 of Two, 16-20).
func sitesWorkspace(t *testing.T) *query.Ctx {
	t.Helper()
	c := tempWorkspace(t, map[string]string{
		"go.mod": "module x/m\n",
		"m.go": "package m\n\nfunc Data() string { return \"\" }\n\nfunc Use() string {\n\ta := Data()\n\tb := Data()\n" +
			"\tc := 1\n\t_ = c\n\td := Data()\n\treturn a + b + d\n}\n\ntype Box struct{}\n\nfunc Two() {\n\tvar a Box\n\tvar b Box\n\t_, _ = a, b\n}\n",
	})
	if err := index.ResolvePending(c.Store, index.Options{Workspace: "w"}); err != nil {
		t.Fatal(err)
	}
	return c
}

// callers renders the callers of Data under a context.
func callers(t *testing.T, c *query.Ctx, x query.Context) string {
	t.Helper()
	c.Context = x
	r, err := query.Callers(c, "Data", query.GraphOpts{Depth: 1})
	return run(t, r, err)
}

func TestCallSitesPrintTheLineOfTheCall(t *testing.T) {
	c := sitesWorkspace(t)
	// A caller is listed once, with the line of every call it makes.
	if got := callers(t, c, query.Context{}); got != "== callers of Data (1)\nUse  m.go:6\n6\t\ta := Data()\n"+
		"7\t\tb := Data()\n10\t\td := Data()\n" {
		t.Fatalf("%q", got)
	}
	// JSON keeps the signature beside the fragments.
	r, err := query.Callers(c, "Data", query.GraphOpts{Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	e := r.Sections[0].Entries[0]
	if e.Text != "func Use() string" || len(e.Fragments) != 2 || e.Fragments[0].Start != 6 || e.Fragments[0].End != 7 ||
		e.Fragments[1].Text != "\td := Data()\n" {
		t.Fatalf("%+v", e)
	}
	// A single call is printed on the line of its caller.
	r, err = query.Callers(c, "Use", query.GraphOpts{Depth: 1})
	if got := run(t, r, err); got != "== callers of Use (0)\n" {
		t.Fatalf("%q", got)
	}
	r, err = query.Refs(c, "Box", query.GraphOpts{})
	if got := run(t, r, err); got != "== references to Box (2)\nm.go\n  Two  :17  var a Box\n  Two  :18  var b Box\n" {
		t.Fatalf("%q", got)
	}
}

func TestCallSitesWithContextMergeAndStayInTheirSymbol(t *testing.T) {
	c := sitesWorkspace(t)
	// Fragments that overlap or touch are printed once; a later one of
	// the same symbol stays apart.
	if got := callers(t, c, query.Context{After: 1}); got != "== callers of Data (1)\nUse  m.go:6\n6\t\ta := Data()\n"+
		"7\t\tb := Data()\n8\t\tc := 1\n10\t\td := Data()\n11\t\treturn a + b + d\n" {
		t.Fatalf("-A 1: %q", got)
	}
	// Entries of one symbol whose fragments meet become one.
	c.Context = query.Context{After: 1}
	r, err := query.Refs(c, "Box", query.GraphOpts{})
	if got := run(t, r, err); got != "== references to Box (1)\nTwo  m.go:17\n17\t\tvar a Box\n18\t\tvar b Box\n19\t\t_, _ = a, b\n" {
		t.Fatalf("refs -A 1: %q", got)
	}
	// Context never leaves the enclosing symbol...
	got := callers(t, c, query.Context{Before: 20, After: 20})
	if !strings.HasPrefix(got, "== callers of Data (1)\nUse  m.go:6\n5\tfunc Use() string {\n") || !strings.HasSuffix(got, "\n12\t}\n") {
		t.Fatalf("-C 20: %q", got)
	}
	// ...unless the file is asked for, and symbol is the symbol whole.
	if file := callers(t, c, query.Context{Before: 20, File: true}); !strings.Contains(file, "\n1\tpackage m\n") ||
		strings.Contains(file, "\n11\t") {
		t.Fatalf("--context file -B 20: %q", file)
	}
	if sym := callers(t, c, query.Context{Symbol: true}); sym != got {
		t.Fatalf("--context symbol: %q", sym)
	}
}

func TestCallSiteOfAChangedFileFallsBackToTheSignature(t *testing.T) {
	c := sitesWorkspace(t)
	t.Setenv("CODEDOC_FRESH_MAX_FILES", "0")
	writeAppend(t, filepath.Join(c.Workspace.Repos[0].Path, "m.go"), "\nfunc Later() {}\n")
	got := callers(t, c, query.Context{})
	if !strings.Contains(got, "\nUse  m.go:6  func Use() string  [stale]\n") || strings.Contains(got, "a := Data()") {
		t.Fatalf("%q", got)
	}
}

func TestParseContext(t *testing.T) {
	for _, tc := range []struct {
		value         string
		before, after int
		want          query.Context
	}{
		{"", -1, -1, query.Context{}},
		{"2", -1, -1, query.Context{Before: 2, After: 2, Set: true}},
		{"2", 0, 5, query.Context{After: 5, Set: true}},
		{"", 0, -1, query.Context{Set: true}},
		{"symbol", -1, -1, query.Context{Symbol: true, Set: true}},
		{"file", 3, -1, query.Context{Before: 3, File: true, Set: true}},
	} {
		if got, err := query.ParseContext(tc.value, tc.before, tc.after); err != nil || got != tc.want {
			t.Errorf("%q -B %d -A %d: %+v %v", tc.value, tc.before, tc.after, got, err)
		}
	}
	for _, bad := range []string{"-1", "lines", "2.5"} {
		if _, err := query.ParseContext(bad, -1, -1); err == nil || !strings.Contains(err.Error(), "symbol or file") {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestLinesTakesSeveralRanges(t *testing.T) {
	c := workspaceOf(t, "go-lib")
	// Ranges are printed in line order, those that overlap or touch as
	// one, each under its own header.
	got := out(t, must(query.Lines(c, "go-lib:store/store.go", "26-27", "10-12", "28", "11+3")))
	if got != "store/store.go\n  :10-14  in Store (10-15)\n10\ttype Store interface {\n11\t\t// Get returns the value for key.\n"+
		"12\t\tGet(key string) (string, error)\n13\t\t// Put stores value under key.\n14\t\tPut(key, value string) error\n"+
		"  :26-28  in MemStore.Get (26-32)\n26\tfunc (m *MemStore) Get(key string) (string, error) {\n"+
		"27\t\tv, ok := m.data[key]\n28\t\tif !ok {\n" {
		t.Fatalf("%q", got)
	}
	if _, err := query.Lines(c, "go-lib:store/store.go", "1-2", "90-95"); err == nil || !strings.Contains(err.Error(), "past the end") {
		t.Fatalf("a range past the end: %v", err)
	}
	if _, err := query.Lines(c, "go-lib:store/store.go"); err == nil {
		t.Fatal("no range accepted")
	}
}

func TestLsListsTheFilesOfADirectoryTogether(t *testing.T) {
	c := tempWorkspace(t, map[string]string{"a/one.go": "package a\n", "b/two.go": "package b\n\nvar X = 1\n",
		"a/three.go": "package a\n\n\n\nvar Y = 1\n", "top.go": "package top\n"})
	sec := ls(t, c, query.LsOpts{Sort: "lines"}, "r:.").Sections[0]
	// Largest first: the directory of the largest file leads, and takes
	// its other files with it.
	if got := locs(sec); got != "a/three.go a/one.go b/two.go top.go" {
		t.Fatalf("%s", got)
	}
	text := render(t, ls(t, c, query.LsOpts{Sort: "lines"}, "r:."))
	if !strings.Contains(text, "== ls r:. (4)\na/\n  three.go  ") || !strings.Contains(text, "  5 lines\n  one.go  ") ||
		!strings.Contains(text, "\nb/two.go  ") || !strings.Contains(text, "\ntop.go  ") {
		t.Fatal(text)
	}
}

// A line that holds two references to a symbol is listed once; when
// they differ in kind, --detail shows both kinds on that one line.
func TestRefsListALineOnce(t *testing.T) {
	c := workspaceOf(t, "go-lib")
	c.Repo, c.Cwd = c.Workspace.Repos[0], c.Workspace.Repos[0].Path
	r, err := query.Refs(c, "store/store.go#MemStore", query.GraphOpts{})
	got := run(t, r, err)
	mustContain(t, got, "== references to MemStore (3)\nstore/store.go\n"+
		"  NewMemStore  :23  func NewMemStore() *MemStore { return &MemStore{data: map[string]string{}} }\n"+
		"  MemStore.Get  :26  ")
	if strings.Count(got, "NewMemStore  :23") != 1 {
		t.Fatalf("a line listed twice:\n%s", got)
	}
	r.Detail = true
	mustContain(t, render(t, r), "  NewMemStore  :23  func NewMemStore() *MemStore  NewMemStore creates an empty MemStore.  [type] [instantiate] [local]\n")
	var b bytes.Buffer
	if _, err := output.Render(&b, r, "json", 0); err != nil || !strings.Contains(b.String(), `"ref_kinds":["type","instantiate"]`) {
		t.Fatalf("%v\n%s", err, b.String())
	}
}

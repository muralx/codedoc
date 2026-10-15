package query_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/query"
	"github.com/muralx/codedoc/internal/store"
)

// tempWorkspace indexes one repo "r" made of the given files.
func tempWorkspace(t *testing.T, files map[string]string) *query.Ctx {
	t.Helper()
	root := t.TempDir()
	for p, body := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ws := &config.Workspace{Name: "w", Repos: []*config.Repo{{Name: "r", Path: root}}}
	s, err := store.Open(filepath.Join(t.TempDir(), "w.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if _, err := index.Repo(s, ws.Repos[0], index.Options{Workspace: ws.Name}); err != nil {
		t.Fatal(err)
	}
	return &query.Ctx{Store: s, Workspace: ws}
}

func TestOutlineKindPrefixAndSingularMember(t *testing.T) {
	c := tempWorkspace(t, map[string]string{
		"a.ts": "export class A {\n  x = 1;\n}\n",
		"b.go": "package b\n\n// V is a value.\nvar V = 1\n",
	})
	r, err := query.Outline(c, "r:a.ts", query.OutlineOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if text := render(t, r); !strings.Contains(text, "== a.ts, 3 lines (1)\nA  :1-3  export class A  [+1 member]\n") ||
		!strings.Contains(keyed(t, r), ":1-3  export class A  [doc:missing] [+1 member]\n") {
		t.Fatal(text)
	}
	r, err = query.Outline(c, "r:b.go", query.OutlineOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if text := render(t, r); !strings.Contains(text, "\nV  :4  var V = 1  V is a value.\n") {
		t.Fatal(text)
	}
}

func TestOutlineCountsGrandchildrenInCollapsedMembers(t *testing.T) {
	c := tempWorkspace(t, map[string]string{"g.go": "package g\n\n\n\n\n\n\n\n\n\n"})
	repo, err := c.Store.RepoByPath(c.Workspace.Repos[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := c.Store.FileByPath(repo.ID, "g.go")
	if err != nil {
		t.Fatal(err)
	}
	mk := func(q, container string, start, end int) store.Symbol {
		return store.Symbol{Kind: "struct", Name: q[strings.LastIndex(q, ".")+1:], Qualified: q, Container: container,
			StableKey: "r:go:.:" + q, StartLine: start, EndLine: end, SigStartLine: start, SigEndLine: start,
			Signature: "type " + q, Visibility: "public", BodyHash: "b"}
	}
	syms := []store.Symbol{mk("T", "", 1, 10), mk("T.A", "T", 2, 8), mk("T.A.x", "T.A", 3, 3), mk("T.A.y", "T.A", 4, 4)}
	if _, err := c.Store.ReplaceFileSymbols(f.ID, 0, syms); err != nil {
		t.Fatal(err)
	}
	r, err := query.Outline(c, "r:g.go", query.OutlineOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if text := render(t, r); len(r.Sections[0].Entries) != 1 || !strings.Contains(text, "[+3 members]") {
		t.Fatal(text)
	}
}

func TestFindPatternsWithSlashMatchQualifiedNames(t *testing.T) {
	c := indexedWorkspace(t)
	for _, pattern := range []string{"records.ts/MemoryRepo", "src/*/MemoryRepo", "src/records.ts/MemoryRepo"} {
		r, err := query.Find(c, pattern, query.FindOpts{})
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(names(r.Sections[0]), ","); got != "src/records.ts/MemoryRepo" {
			t.Errorf("%s: %q", pattern, got)
		}
	}
}

func TestOutlinePathsResolveFromCwdFirst(t *testing.T) {
	c := indexedWorkspace(t)
	goLib := c.Workspace.Repos[0]
	c.Repo, c.Cwd = goLib, filepath.Join(goLib.Path, "store")
	for target, title := range map[string]string{
		"store.go":                          "store/store.go, 38 lines", // cwd-relative
		"store/null.go":                     "store/null.go, 8 lines",   // not below cwd: repo-root relative
		".":                                 "store",
		"../service":                        "service",
		filepath.Join(goLib.Path, "go.mod"): "go.mod",
	} {
		r, err := query.Outline(c, target, query.OutlineOpts{})
		if err != nil || len(r.Sections) != 1 || !strings.HasPrefix(r.Sections[0].Title, title) {
			t.Errorf("%s: %+v %v", target, r, err)
		}
	}
	c.Repo, c.Cwd = nil, filepath.Dir(goLib.Path)
	if r, err := query.Outline(c, "go-lib/store", query.OutlineOpts{}); err != nil || len(r.Sections) != 1 || r.Sections[0].Title != "store" {
		t.Fatalf("%+v %v", r, err)
	}
}

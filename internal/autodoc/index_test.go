package autodoc

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/query"
	"github.com/muralx/codedoc/internal/store"
	"github.com/muralx/codedoc/internal/testutil"
)

// indexedRepo indexes a copy of the java-lib fixture.
func indexedRepo(t *testing.T) Indexed {
	t.Helper()
	ws := &config.Workspace{Name: "fx", Repos: []*config.Repo{{Name: "java-lib", Path: testutil.CopyFixture(t, "java-lib")}}}
	s, err := store.Open(filepath.Join(t.TempDir(), "ws.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	opts := index.Options{Workspace: "fx", RepoOrder: ws.RepoNames()}
	if _, err := index.Repo(s, ws.Repos[0], opts); err != nil {
		t.Fatal(err)
	}
	if err := index.ResolvePending(s, opts); err != nil {
		t.Fatal(err)
	}
	return Indexed{Ctx: &query.Ctx{Store: s, Workspace: ws}}
}

func TestIndexedServesGapsReindexAndStates(t *testing.T) {
	x := indexedRepo(t)
	gaps, err := x.Next("java-lib", 2)
	if err != nil || len(gaps) != 2 || gaps[0].Key == "" || gaps[0].Module == "" || gaps[0].State == "" {
		t.Fatalf("%v %+v", err, gaps)
	}
	states, err := x.States([]string{gaps[0].Key})
	if err != nil || states[gaps[0].Key] != gaps[0].State {
		t.Fatalf("%v %v", err, states)
	}
}

func TestIndexedReindexesFiles(t *testing.T) {
	x := indexedRepo(t)
	all, err := x.Next("java-lib", 0)
	if err != nil {
		t.Fatal(err)
	}
	g := all[slices.IndexFunc(all, func(g Gap) bool { return !strings.HasSuffix(g.Key, "/") })]
	root := x.Ctx.Workspace.Repos[0].Path
	p := filepath.Join(root, filepath.FromSlash(g.Path))
	src, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(strings.Replace(string(src), "\n", "\n\n", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := x.Reindex("java-lib", []string{g.Path}); err != nil {
		t.Fatal(err)
	}
	if err := x.Reindex("java-lib", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := x.Next("nope", 1); err == nil {
		t.Fatal("unknown repo accepted")
	}
	if err := x.Reindex("nope", []string{"a"}); err == nil {
		t.Fatal("unknown repo accepted")
	}
}

func TestIndexedDocs(t *testing.T) {
	x := indexedRepo(t)
	docs, err := x.Docs("java-lib")
	if err != nil || len(docs) == 0 {
		t.Fatalf("%v %d", err, len(docs))
	}
	if documented, module := docKinds(docs); !documented || !module {
		t.Errorf("documented symbol %v, module %v in %v", documented, module, docs)
	}
	if _, err := x.Docs("nope"); err == nil {
		t.Error("unknown repo accepted")
	}
	x.Ctx.Workspace.Repos = append(x.Ctx.Workspace.Repos, &config.Repo{Name: "new", Path: t.TempDir()})
	if d, err := x.Docs("new"); err != nil || len(d) != 0 {
		t.Errorf("unindexed repo: %v %v", d, err)
	}
}

// docKinds reports whether docs hold a documented symbol and a module.
func docKinds(docs map[string]DocAt) (documented, module bool) {
	for k, d := range docs {
		documented = documented || (d.Hash != "" && d.Path != "" && !strings.HasSuffix(k, "/"))
		module = module || strings.HasPrefix(k, "java-lib:") && strings.HasSuffix(k, "/")
	}
	return documented, module
}

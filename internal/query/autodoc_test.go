package query_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/query"
)

func TestRepoGapsRanksWritableGaps(t *testing.T) {
	c := workspaceOf(t, "go-lib", "java-lib")
	r := c.Workspace.Repos[1]
	all, err := query.RepoGaps(c, r, 0)
	if err != nil || len(all) < 3 {
		t.Fatalf("%v %d", err, len(all))
	}
	two, err := query.RepoGaps(c, r, 2)
	if err != nil || len(two) != 2 || two[0].Key != all[0].Key {
		t.Fatalf("%v %+v", err, two)
	}
	keys := []string{all[0].Key, "go-lib:store/", "nope:go:x:Y"}
	states, err := query.DocStates(c, keys)
	if err != nil {
		t.Fatal(err)
	}
	if states[all[0].Key] != string(all[0].State) || states["go-lib:store/"] != "ok" || len(states) != 2 {
		t.Fatalf("%v", states)
	}
}

func TestDaemonFreshSkipsTheStatPass(t *testing.T) {
	c := workspaceOf(t, "go-lib")
	r := c.Workspace.Repos[0]
	src := "package store\n\nfunc AddedLater() {}\n"
	if err := os.WriteFile(filepath.Join(r.Path, "store", "later.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	asked := 0
	c.DaemonFresh = func(ws string, repo *config.Repo) bool {
		asked++
		return ws == "fixture" && repo == r
	}
	if err := c.EnsureFresh(r); err != nil {
		t.Fatal(err)
	}
	f := c.Fresh["go-lib"]
	if asked != 1 || !f.Fresh || f.Branch != "main" || f.Commit == "" || f.Dirty != 1 || f.Changed != 0 {
		t.Fatalf("asked %d: %+v", asked, f)
	}
	if found(t, c, "AddedLater", "later.go") {
		t.Fatal("stat pass ran although the daemon reported fresh")
	}
	c.Fresh, c.DaemonFresh = nil, func(string, *config.Repo) bool { return false }
	if !found(t, c, "AddedLater", "later.go") {
		t.Fatal("stat pass skipped although the daemon reported stale")
	}
}

// found reports whether the find answer for name mentions path.
func found(t *testing.T, c *query.Ctx, name, path string) bool {
	t.Helper()
	resp, err := query.Find(c, name, query.FindOpts{})
	if err != nil {
		t.Fatal(err)
	}
	return strings.Contains(render(t, resp), path)
}

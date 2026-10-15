package query_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/deps"
	"github.com/muralx/codedoc/internal/query"
	"github.com/muralx/codedoc/internal/store"
	"github.com/muralx/codedoc/internal/testutil"
)

// depStates returns "name version state" of the workspace's uses.
func depStates(t *testing.T, c *query.Ctx) string {
	t.Helper()
	uses, err := c.Store.DepUses(0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, u := range uses {
		if u.State != store.DepMissing && u.State != store.DepSkipped {
			out = append(out, u.Name+" "+u.Version+" "+u.State)
		}
	}
	return strings.Join(out, ", ")
}

// A query's freshness check discovers the dependencies of the repos it
// touches and indexes the missing package versions, within its budget.
func TestEnsureFreshIndexesDependencies(t *testing.T) {
	t.Setenv(deps.ModCacheEnv, testutil.ModCacheDir())
	c := workspaceOf(t, "deps-go", "go-lib")
	c.DepsDB = filepath.Join(t.TempDir(), "deps.db")
	if _, err := query.Find(c, "main", query.FindOpts{}); err != nil {
		t.Fatal(err)
	}
	want := "github.com/BurntSushi/toml v0.3.1 unused, github.com/acme/kit v1.4.0 indexed"
	if got := depStates(t, c); got != want || c.DepsPending != 0 {
		t.Fatalf("got %q, pending %d", got, c.DepsPending)
	}
	db, err := deps.Open(c.DepsDB)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if has, err := db.Has("go/github.com/acme/kit@v1.4.0"); err != nil || !has {
		t.Fatalf("kit is not in deps.db: %v", err)
	}
}

// Without a deps.db path a query leaves dependencies alone, and a
// deps.db that cannot be opened is noted on the answer, not fatal.
func TestEnsureFreshWithoutOrWithABrokenDepsDB(t *testing.T) {
	t.Setenv(deps.ModCacheEnv, testutil.ModCacheDir())
	c := workspaceOf(t, "deps-go")
	if _, err := query.Find(c, "main", query.FindOpts{}); err != nil || depStates(t, c) != "" {
		t.Fatalf("no deps.db path: %v %q", err, depStates(t, c))
	}
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	c.DepsDB = filepath.Join(blocked, "deps.db")
	resp, err := query.Find(c, "main", query.FindOpts{})
	if err != nil || !strings.Contains(resp.Note, "deps: ") {
		t.Fatalf("a broken deps.db is a note: %v %+v", err, resp)
	}
	if got := depStates(t, c); !strings.Contains(got, "github.com/acme/kit v1.4.0 pending") || c.DepsPending != 1 {
		t.Fatalf("got %q, pending %d", got, c.DepsPending)
	}
}

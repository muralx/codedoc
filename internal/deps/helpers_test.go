package deps_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/deps"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/store"
	"github.com/muralx/codedoc/internal/testutil"
)

// space is a test workspace: copies of fixture repos, indexed, with a
// workspace database and a deps.db of its own.
type space struct {
	t     *testing.T
	ws    *config.Workspace
	store *store.Store
	opts  deps.Options
}

// newSpace copies the named fixtures into a workspace, indexes them and
// selects the fixture module cache.
func newSpace(t *testing.T, fixtures ...string) *space {
	t.Helper()
	t.Setenv(deps.ModCacheEnv, testutil.ModCacheDir())
	sp := &space{t: t, ws: &config.Workspace{Name: "fx"}}
	for _, name := range fixtures {
		sp.ws.Repos = append(sp.ws.Repos, &config.Repo{Name: name, Path: testutil.CopyFixture(t, name)})
	}
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "fx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	sp.store, sp.opts = s, deps.Options{DB: filepath.Join(dir, "deps.db"), Full: true}
	sp.index()
	return sp
}

// index indexes every repo of the workspace.
func (sp *space) index() {
	sp.t.Helper()
	opts := index.Options{Workspace: sp.ws.Name, RepoOrder: sp.ws.RepoNames()}
	for _, r := range sp.ws.Repos {
		if _, err := index.Repo(sp.store, r, opts); err != nil {
			sp.t.Fatal(err)
		}
	}
}

// sync runs a Sync over the whole workspace with opts.
func (sp *space) sync(opts deps.Options) deps.Summary {
	sp.t.Helper()
	sum, err := deps.Sync(context.Background(), sp.store, sp.ws, sp.ws.Repos, opts)
	if err != nil {
		sp.t.Fatal(err)
	}
	return sum
}

// repo returns the path of a repo copy.
func (sp *space) repo(name string) string {
	for _, r := range sp.ws.Repos {
		if r.Name == name {
			return r.Path
		}
	}
	sp.t.Fatalf("no repo %s", name)
	return ""
}

// uses returns the dependency uses of the workspace keyed by
// "manifest name".
func (sp *space) uses() map[string]store.DepUse {
	sp.t.Helper()
	all, err := sp.store.DepUses(0)
	if err != nil {
		sp.t.Fatal(err)
	}
	out := map[string]store.DepUse{}
	for _, u := range all {
		out[u.Manifest+" "+u.Name] = u
	}
	return out
}

// db opens the workspace's deps.db.
func (sp *space) db() *deps.DB {
	sp.t.Helper()
	db, err := deps.Open(sp.opts.DB)
	if err != nil {
		sp.t.Fatal(err)
	}
	sp.t.Cleanup(func() { _ = db.Close() })
	return db
}

// write replaces a file of a repo copy.
func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// eq fails the test unless got equals want.
func eq(t *testing.T, what string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s:\n got  %+v\n want %+v", what, got, want)
	}
}

// states returns "version state" of every use, keyed like uses; a
// missing or skipped use has no version.
func (sp *space) states() map[string]string {
	out := map[string]string{}
	for key, u := range sp.uses() {
		out[key] = strings.TrimSpace(u.Version + " " + u.State)
	}
	return out
}

// dirs returns the directory of every use relative to root, "" for a
// use without one.
func (sp *space) dirs(root string) map[string]string {
	out := map[string]string{}
	for key, u := range sp.uses() {
		out[key] = strings.TrimPrefix(strings.TrimPrefix(u.Dir, root), "/")
	}
	return out
}

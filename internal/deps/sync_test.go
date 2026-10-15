package deps_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/muralx/codedoc/internal/deps"
	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/store"
)

func TestSyncIndexesEachVersionOnce(t *testing.T) {
	sp := newSpace(t, "deps-app", "deps-go")
	eq(t, "first sync", sp.sync(sp.opts), deps.Summary{Indexed: 6})
	eq(t, "states", sp.states(), map[string]string{
		"package.json @acme/sdk":              "1.2.0 indexed",
		"package.json dup":                    "2.0.0 indexed",
		"package.json linked-lib":             "0.3.0 indexed",
		"package.json plain-js":               "2.0.1 unused",
		"package.json lint-tool":              "9.4.0 unused",
		"package.json @types/node":            "22.1.0 indexed",
		"package.json left-pad":               "missing",
		"package.json @fixture/web":           "skipped",
		"packages/web/package.json @acme/sdk": "1.2.0 unused",
		"packages/web/package.json dup":       "1.0.0 indexed",
		"go.mod github.com/acme/kit":          "v1.4.0 indexed",
		"go.mod github.com/BurntSushi/toml":   "v0.3.1 unused",
		"go.mod github.com/acme/absent":       "missing",
		"go.mod github.com/fixture/lib":       "missing",
	})
	eq(t, "second sync", sp.sync(sp.opts), deps.Summary{})
	// Another workspace using the same versions finds them in deps.db.
	other := newSpace(t, "deps-app")
	other.opts.DB = sp.opts.DB
	eq(t, "other workspace", other.sync(other.opts), deps.Summary{})
	eq(t, "shared version", other.states()["package.json dup"], "2.0.0 indexed")
}

// On the query path indexing stops at the deadline and what is left is
// reported as pending.
func TestSyncStopsAtTheDeadline(t *testing.T) {
	sp := newSpace(t, "deps-app")
	late := deps.Options{DB: sp.opts.DB, Deadline: time.Unix(1, 0)}
	eq(t, "past the deadline", sp.sync(late), deps.Summary{Pending: 5})
	n, err := deps.Pending(sp.store, sp.ws.Repos)
	eq(t, "error", err, nil)
	eq(t, "pending", n, 5)
	eq(t, "without a deadline", sp.sync(deps.Options{DB: sp.opts.DB}), deps.Summary{Indexed: 5})
}

// On the query path discovery reruns only when a manifest, a lock file
// or an install marker changed.
func TestSyncRediscoversOnlyOnTrackedChanges(t *testing.T) {
	sp := newSpace(t, "deps-app")
	quick := deps.Options{DB: sp.opts.DB}
	sp.sync(quick)
	dup := filepath.Join(sp.repo("deps-app"), "node_modules/dup/package.json")
	write(t, dup, `{"name": "dup", "version": "2.1.0", "typings": "dup.d.ts"}`)
	sp.sync(quick)
	eq(t, "no tracked file changed", sp.states()["package.json dup"], "2.0.0 indexed")
	write(t, filepath.Join(sp.repo("deps-app"), "pnpm-lock.yaml"), "lockfileVersion: '9.0'\n# dup 2.1.0\n")
	eq(t, "a lock file change", sp.sync(quick), deps.Summary{Indexed: 1})
	eq(t, "the new version", sp.states()["package.json dup"], "2.1.0 indexed")
	eq(t, "an unchanged version keeps its state", sp.states()["package.json @acme/sdk"], "1.2.0 indexed")
	write(t, filepath.Join(sp.repo("deps-app"), "node_modules/.modules.yaml"), "layoutVersion: 5\n# reinstalled\n")
	write(t, dup, `{"name": "dup", "version": "2.2.0", "typings": "dup.d.ts"}`)
	eq(t, "an install marker change", sp.sync(quick), deps.Summary{Indexed: 1})
}

// A Go module downloaded after discovery recorded it missing is found
// without any change to go.mod.
func TestSyncFindsAModuleThatAppeared(t *testing.T) {
	sp := newSpace(t, "deps-go")
	sp.ws.Deps.Include = []string{"github.com/acme/absent"}
	cache := t.TempDir()
	if err := os.CopyFS(cache, os.DirFS(os.Getenv(deps.ModCacheEnv))); err != nil {
		t.Fatal(err)
	}
	t.Setenv(deps.ModCacheEnv, cache)
	quick := deps.Options{DB: sp.opts.DB}
	sp.sync(quick)
	if u := sp.uses()["go.mod github.com/acme/absent"]; u.State != store.DepMissing {
		t.Fatalf("%+v", u)
	}
	write(t, filepath.Join(cache, "github.com/acme/absent@v0.1.0/absent.go"), "package absent\n\n// Here is present now.\nfunc Here() {}\n")
	if sum := sp.sync(quick); sum.Indexed != 1 {
		t.Fatalf("%+v", sum)
	}
	if u := sp.uses()["go.mod github.com/acme/absent"]; u.State != store.DepIndexed || u.Version != "v0.1.0" {
		t.Fatalf("%+v", u)
	}
}

// A package that cannot be extracted is recorded as failed with the
// error, leaves no rows, and is retried by a full sync only.
func TestSyncRecordsFailuresAndRetriesThemWhenFull(t *testing.T) {
	sp := newSpace(t, "deps-app")
	quick := deps.Options{DB: sp.opts.DB}
	sp.sync(deps.Options{DB: sp.opts.DB, Deadline: time.Unix(1, 0)})
	manifest := filepath.Join(sp.repo("deps-app"), "node_modules/dup/package.json")
	good, _ := os.ReadFile(manifest)
	write(t, manifest, "{broken")
	sum := sp.sync(quick)
	u := sp.uses()["package.json dup"]
	if sum.Failed != 1 || sum.Indexed != 4 || u.State != store.DepFailed || !strings.Contains(u.Error, "package.json") {
		t.Fatalf("%+v %+v", sum, u)
	}
	if has, _ := sp.db().Has("npm/dup@2.0.0"); has {
		t.Fatal("a failed package leaves no rows")
	}
	write(t, manifest, string(good))
	if sum := sp.sync(quick); sum.Indexed != 0 || sum.Failed != 0 {
		t.Fatalf("a failed package is not retried on the query path: %+v", sum)
	}
	if sum := sp.sync(sp.opts); sum.Indexed != 1 {
		t.Fatalf("a full sync retries it: %+v", sum)
	}
	if u := sp.uses()["package.json dup"]; u.State != store.DepIndexed || u.Error != "" {
		t.Fatalf("%+v", u)
	}
}

func TestWriteIsOneTransaction(t *testing.T) {
	sp := newSpace(t, "deps-app")
	db := sp.db()
	id := deps.ID{Eco: "npm", Name: "broken", Version: "1.0.0"}
	twice := []extract.Symbol{{Kind: extract.KindFunction, Name: "f", Qualified: "a.d.ts/f"}}
	ex := &deps.Extraction{
		Modules: []deps.Module{{Path: ".", Lang: "ts", Kind: "npm-package", Identity: "broken", Doc: "readme", DocSource: "README.md"}},
		Files: []deps.File{{Path: "a.d.ts", Lang: "typescript", Symbols: twice},
			{Path: "b.d.ts", Lang: "typescript", Symbols: twice}},
	}
	if _, err := db.Write(id, ex); err == nil {
		t.Fatal("two symbols with one stable key must fail")
	}
	if has, _ := db.Has(id.String()); has {
		t.Fatal("the failed write left its repo row")
	}
	if syms, _ := db.Store().SymbolsByName("f", "", 0); len(syms) != 0 {
		t.Fatalf("the failed write left symbols: %+v", syms)
	}
	ex.Files = ex.Files[:1]
	if wrote, err := db.Write(id, ex); err != nil || !wrote {
		t.Fatalf("%v %v", wrote, err)
	}
	if wrote, err := db.Write(id, ex); err != nil || wrote {
		t.Fatalf("a version is written once: %v %v", wrote, err)
	}
}

// Prune asks the workspace databases recorded in deps.db what they use:
// a package goes only when none of them, and no workspace of the
// config, resolves to it.
func TestPruneKeepsWhatRecordedWorkspacesUse(t *testing.T) {
	sp := newSpace(t, "deps-app")
	sp.sync(sp.opts)
	db := sp.db()
	out, err := db.Prune(deps.PruneOptions{})
	eq(t, "error", err, nil)
	eq(t, "every version is used by the recorded workspace", out.Packages, []string(nil))
	eq(t, "the workspace database is a recorded user", out.Users, []string{sp.store.Path()})
	sp.ws.Deps.Exclude = []string{"dup", "linked-lib"}
	sp.sync(sp.opts)
	unused := []string{"npm/dup@1.0.0", "npm/dup@2.0.0", "npm/linked-lib@0.3.0"}
	out, err = db.Prune(deps.PruneOptions{DryRun: true})
	eq(t, "dry run lists the unused versions", out.Packages, unused)
	eq(t, "error", err, nil)
	if left, _ := db.Packages(); len(left) != 5 {
		t.Fatalf("a dry run deleted: %+v", left)
	}
	out, err = db.Prune(deps.PruneOptions{Keep: func() (map[string]bool, error) {
		return map[string]bool{"npm/dup@2.0.0": true}, nil
	}})
	eq(t, "what the config's workspaces use is kept too", out.Packages, []string{"npm/dup@1.0.0", "npm/linked-lib@0.3.0"})
	eq(t, "error", err, nil)
	out, err = db.Prune(deps.PruneOptions{All: true})
	eq(t, "all empties the database", len(out.Packages), 3)
	eq(t, "error", err, nil)
}

// A package pruned behind the back of a workspace is noticed by its next
// sync, the bounded one of a query included: the use becomes pending and
// is indexed again.
func TestSyncHealsUsesWhosePackagesVanished(t *testing.T) {
	sp := newSpace(t, "deps-app")
	sp.sync(sp.opts)
	db := sp.db()
	if out, err := db.Prune(deps.PruneOptions{All: true}); err != nil || len(out.Packages) != 5 {
		t.Fatalf("%+v %v", out, err)
	}
	eq(t, "the uses still say indexed", sp.states()["package.json dup"], "2.0.0 indexed")
	sum := sp.sync(deps.Options{DB: sp.opts.DB, Deadline: timePast})
	eq(t, "a sync out of time leaves them pending", []int{sum.Indexed, sum.Pending}, []int{0, 5})
	eq(t, "state", sp.states()["package.json dup"], "2.0.0 pending")
	sum = sp.sync(deps.Options{DB: sp.opts.DB})
	eq(t, "the next one indexes them", []int{sum.Indexed, sum.Pending}, []int{5, 0})
	eq(t, "error", os.Remove(sp.opts.DB), nil)
	sum = sp.sync(deps.Options{DB: sp.opts.DB})
	eq(t, "a deleted deps.db is rebuilt", []int{sum.Indexed, sum.Pending}, []int{5, 0})
}

// Recorded users whose database is gone are forgotten; one that cannot
// be opened is trusted for what it recorded.
func TestPruneForgetsDeletedWorkspacesAndTrustsUnreadableOnes(t *testing.T) {
	sp := newSpace(t, "deps-app")
	sp.sync(sp.opts)
	db := sp.db()
	gone, broken := filepath.Join(t.TempDir(), "gone.db"), filepath.Join(t.TempDir(), "broken.db")
	write(t, broken, "this is no database")
	err := db.Store().Batch(func(b *store.Batch) error {
		if err := b.ReplaceDepUsers(gone, []string{"npm/x@1.0.0"}, 1); err != nil {
			return err
		}
		if err := b.ReplaceDepUsers(sp.store.Path(), nil, 0); err != nil {
			return err
		}
		return b.ReplaceDepUsers(broken, []string{"npm/dup@1.0.0"}, 1)
	})
	eq(t, "error", err, nil)
	out, err := db.Prune(deps.PruneOptions{})
	eq(t, "error", err, nil)
	eq(t, "kept for the unreadable user", out.Users, []string{broken})
	eq(t, "forgotten", out.Gone, []string{gone})
	eq(t, "pruned all but what the unreadable user recorded", len(out.Packages), 4)
	users, err := db.Store().DepUsers("")
	eq(t, "error", err, nil)
	eq(t, "users left", len(users), 1)
}

func TestSyncClearsUsesWhenDisabledAndSkipsUnindexedRepos(t *testing.T) {
	sp := newSpace(t, "deps-app")
	sp.sync(sp.opts)
	off := false
	sp.ws.Deps.Enabled = &off
	if sum := sp.sync(sp.opts); sum.Pending != 0 {
		t.Fatalf("%+v", sum)
	}
	if uses := sp.uses(); len(uses) != 0 {
		t.Fatalf("uses left with deps disabled: %+v", uses)
	}
	sp.ws.Deps.Enabled = nil
	gone := *sp.ws.Repos[0]
	gone.Path, gone.Name = filepath.Join(t.TempDir(), "absent"), "absent"
	never := *sp.ws.Repos[0]
	never.Path, never.Name = t.TempDir(), "never-indexed"
	sp.ws.Repos = append(sp.ws.Repos, &gone, &never)
	if sum := sp.sync(sp.opts); sum.Pending != 0 || len(sp.uses()) == 0 {
		t.Fatalf("%+v", sum)
	}
}

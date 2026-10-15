package store_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/muralx/codedoc/internal/store"
)

func seedDeps(t *testing.T, s *store.Store) (repoA, repoB int64) {
	t.Helper()
	repoA, _ = s.UpsertRepo(store.Repo{Workspace: "w", Name: "a", Path: "/a"})
	repoB, _ = s.UpsertRepo(store.Repo{Workspace: "w", Name: "b", Path: "/b"})
	inBatch(t, s, func(b *store.Batch) error {
		if err := b.ReplaceDepUses(repoA, []store.DepUse{
			{Manifest: "package.json", Eco: "npm", Name: "zod", Version: "3.23.8", Declared: "^3.23.0", State: store.DepPending, Dir: "/a/node_modules/zod"},
			{Manifest: "package.json", Eco: "npm", Name: "left-pad", Declared: "^1.0.0", State: store.DepMissing},
			{Manifest: "web/package.json", Eco: "npm", Name: "@fx/ui", Version: "workspace", Declared: "workspace:*", State: store.DepSkipped},
		}); err != nil {
			return err
		}
		return b.ReplaceDepUses(repoB, []store.DepUse{
			{Manifest: "go.mod", Eco: "go", Name: "github.com/x/y", Version: "v1.2.0", Declared: "v1.2.0", State: store.DepPending},
			{Manifest: "package.json", Eco: "npm", Name: "zod", Version: "3.23.8", Declared: "3.23.8", State: store.DepPending},
		})
	})
	return repoA, repoB
}

// eq fails the test unless got equals want.
func eq(t *testing.T, what string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: got %+v, want %+v", what, got, want)
	}
}

// states returns "name state" of the uses of a repo (0 for all).
func states(t *testing.T, s *store.Store, repoID int64) []string {
	t.Helper()
	uses, err := s.DepUses(repoID)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(uses))
	for i, u := range uses {
		out[i] = u.Name + " " + u.State
	}
	return out
}

func TestDepUsesRoundTrip(t *testing.T) {
	s := openTemp(t)
	repoA, _ := seedDeps(t, s)
	eq(t, "all", len(states(t, s, 0)), 5)
	one, err := s.DepUses(repoA)
	eq(t, "error", err, nil)
	eq(t, "repo a", states(t, s, repoA), []string{"left-pad missing", "zod pending", "@fx/ui skipped"})
	eq(t, "id", one[1].PackageID(), "npm/zod@3.23.8")
	eq(t, "id of a missing use", one[0].PackageID(), "")
	eq(t, "root", one[1].Root(), "/a/node_modules/zod")
	eq(t, "root with types", store.DepUse{Dir: "/x", TypesDir: "/t"}.Root(), "/t")
	eq(t, "declared", one[1].Declared, "^3.23.0")
	inBatch(t, s, func(b *store.Batch) error { return b.ReplaceDepUses(repoA, nil) })
	eq(t, "after clearing", len(states(t, s, repoA)), 0)
}

func TestDepStatesFollowThePackageVersion(t *testing.T) {
	s := openTemp(t)
	repoA, repoB := seedDeps(t, s)
	inBatch(t, s, func(b *store.Batch) error {
		if err := b.SetDepState("npm", "zod", "3.23.8", store.DepIndexed, ""); err != nil {
			return err
		}
		return b.SetDepState("go", "github.com/x/y", "v1.2.0", store.DepFailed, "boom")
	})
	ids, err := s.DepPackageIDs(0, store.DepIndexed)
	eq(t, "error", err, nil)
	eq(t, "indexed ids", ids, []string{"npm/zod@3.23.8"})
	ids, _ = s.DepPackageIDs(repoB, store.DepFailed)
	eq(t, "failed ids", ids, []string{"go/github.com/x/y@v1.2.0"})
	uses, _ := s.DepUses(repoB)
	eq(t, "error text", uses[0].Error, "boom")
	// Missing and skipped uses keep their state whatever is indexed.
	inBatch(t, s, func(b *store.Batch) error { return b.ResetDepStates() })
	ids, _ = s.DepPackageIDs(0, store.DepPending)
	eq(t, "pending after reset", ids, []string{"npm/zod@3.23.8"})
	eq(t, "repo a", states(t, s, repoA), []string{"left-pad missing", "zod pending", "@fx/ui skipped"})
	eq(t, "repo b", states(t, s, repoB), []string{"github.com/x/y failed", "zod pending"})
}

func TestDepManifestsAndFilesNamed(t *testing.T) {
	s := openTemp(t)
	repo, _ := s.UpsertRepo(store.Repo{Workspace: "w", Name: "a", Path: "/a"})
	for _, p := range []string{"package.json", "web/package.json", "web/xpackage.json", "go.mod", "docs/package_json"} {
		if _, err := s.UpsertFile(store.File{RepoID: repo, Path: p, Size: 1, Mtime: 1, Hash: "h"}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.FilesNamed(repo, "package.json", "go.mod")
	if err != nil || !reflect.DeepEqual(got, []string{"go.mod", "package.json", "web/package.json"}) {
		t.Fatalf("%v %v", got, err)
	}
	want := []store.DepManifest{{Path: "go.mod", Size: 3, Mtime: 4, Hash: "aa"}, {Path: "package.json", Size: 1, Mtime: 2, Hash: "bb"}}
	inBatch(t, s, func(b *store.Batch) error { return b.ReplaceDepManifests(repo, want) })
	if got, err := s.DepManifests(repo); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v %v", got, err)
	}
	inBatch(t, s, func(b *store.Batch) error { return b.ReplaceDepManifests(repo, want[:1]) })
	if got, _ := s.DepManifests(repo); len(got) != 1 {
		t.Fatalf("%+v", got)
	}
}

// seedPackage writes one package version the way deps.db holds it.
func seedPackage(t *testing.T, s *store.Store) (id int64) {
	t.Helper()
	inBatch(t, s, func(b *store.Batch) error {
		var err error
		id, err = b.UpsertRepo(store.Repo{Workspace: "deps", Name: "npm/zod@3.23.8", Path: "npm/zod@3.23.8", VCS: "npm", Commit: "3.23.8"})
		if err != nil {
			return err
		}
		root, err := b.UpsertModule(store.Module{RepoID: id, Path: ".", Lang: "ts", Kind: "npm-package", Identity: "zod"})
		if err != nil {
			return err
		}
		if _, err := b.UpsertModule(store.Module{RepoID: id, Path: "v4", Lang: "ts", Kind: "npm-export", Identity: "zod/v4", ParentID: root}); err != nil {
			return err
		}
		file, err := b.UpsertFile(store.File{RepoID: id, Path: "index.d.ts", Lang: "typescript", Size: 1, Mtime: 1, Hash: "h"})
		if err != nil {
			return err
		}
		if _, err := b.ReplaceFileSymbols(file, root, []store.Symbol{
			{Kind: "class", Name: "ZodType", Qualified: "index.d.ts/ZodType", StableKey: "npm/zod@3.23.8:ts:zod:index.d.ts/ZodType"},
			{Kind: "method", Name: "parse", Qualified: "index.d.ts/ZodType.parse", Container: "index.d.ts/ZodType",
				StableKey: "npm/zod@3.23.8:ts:zod:index.d.ts/ZodType.parse"},
		}); err != nil {
			return err
		}
		return b.SetRepoIndexedAt(id, 42)
	})
	return id
}

// A package version is one repo row of deps.db, written and deleted
// whole.
func TestPackageRepoRowsAreWrittenAndDeletedWhole(t *testing.T) {
	s := openTemp(t)
	id := seedPackage(t, s)
	row, err := s.RepoByName("deps", "npm/zod@3.23.8")
	eq(t, "error", err, nil)
	eq(t, "row", []any{row.ID, row.VCS, row.Commit, row.IndexedAt}, []any{id, "npm", "3.23.8", int64(42)})
	_, err = s.RepoByName("deps", "npm/zod@4.0.0")
	eq(t, "unknown version", errors.Is(err, store.ErrNotFound), true)
	repos, err := s.Repos()
	eq(t, "error", err, nil)
	eq(t, "repos", len(repos), 1)
	counts, err := s.SymbolCounts()
	eq(t, "error", err, nil)
	eq(t, "symbols", counts[id], 2)
	inBatch(t, s, func(b *store.Batch) error { return b.DeleteRepo(id) })
	repos, _ = s.Repos()
	eq(t, "repos left", len(repos), 0)
	_, err = s.SymbolByKey("npm/zod@3.23.8:ts:zod:index.d.ts/ZodType")
	eq(t, "symbol left", errors.Is(err, store.ErrNotFound), true)
	err = s.Batch(func(b *store.Batch) error { return b.DeleteRepo(id) })
	eq(t, "deleting twice", errors.Is(err, store.ErrNotFound), true)
}

func TestDepReadsFailOnClosedDatabase(t *testing.T) {
	s := openTemp(t)
	_ = s.Close()
	for name, err := range map[string]error{
		"DepUses":       second(s.DepUses(0)),
		"DepPackageIDs": second(s.DepPackageIDs(0, store.DepIndexed)),
		"DepManifests":  second(s.DepManifests(1)),
		"FilesNamed":    second(s.FilesNamed(1, "go.mod")),
		"RepoByName":    second(s.RepoByName("w", "a")),
		"Repos":         second(s.Repos()),
		"SymbolCounts":  second(s.SymbolCounts()),
	} {
		if err == nil {
			t.Errorf("%s: no error on a closed database", name)
		}
	}
}

func second[T any](_ T, err error) error { return err }

package store_test

import (
	"testing"

	"github.com/muralx/codedoc/internal/store"
)

// One use is set unused without touching the other use of the same
// package version; an unused use is nothing deps.db has to keep, and
// the outcome of indexing the version does not reach it.
func TestAnUnusedUseIsSetAloneAndNotKept(t *testing.T) {
	s := openTemp(t)
	repoA, repoB := seedDeps(t, s)
	inBatch(t, s, func(b *store.Batch) error {
		return b.SetDepUseState(store.DepUse{RepoID: repoA, Manifest: "package.json", Eco: "npm", Name: "zod",
			State: store.DepUnused})
	})
	eq(t, "repo a", states(t, s, repoA), []string{"left-pad missing", "zod unused", "@fx/ui skipped"})
	eq(t, "repo b", states(t, s, repoB), []string{"github.com/x/y pending", "zod pending"})
	inBatch(t, s, func(b *store.Batch) error { return b.SetDepState("npm", "zod", "3.23.8", store.DepIndexed, "") })
	eq(t, "indexing leaves the unused use", states(t, s, repoA)[1], "zod unused")
	eq(t, "and reaches the other", states(t, s, repoB)[1], "zod indexed")
	inBatch(t, s, func(b *store.Batch) error {
		return b.SetDepUseState(store.DepUse{RepoID: repoB, Manifest: "package.json", Eco: "npm", Name: "zod",
			State: store.DepUnused})
	})
	used, err := s.UsedDepPackageIDs()
	eq(t, "error", err, nil)
	eq(t, "a version every use of which is unused is not kept", used, []string{"go/github.com/x/y@v1.2.0", "npm/@fx/ui@workspace"})
	indexed, err := s.DepPackageIDs(0, store.DepIndexed)
	eq(t, "error", err, nil)
	eq(t, "nor seen by a query", indexed, []string(nil))
}

// ImportSites lists what the files of one repo import, relative
// specifiers left out.
func TestImportSitesListNonRelativeSpecifiers(t *testing.T) {
	s := openTemp(t)
	repoA, _ := s.UpsertRepo(store.Repo{Workspace: "w", Name: "a", Path: "/a"})
	repoB, _ := s.UpsertRepo(store.Repo{Workspace: "w", Name: "b", Path: "/b"})
	inBatch(t, s, func(b *store.Batch) error {
		for path, raws := range map[string][]string{"src/a.ts": {"zod", "./local", "zod", "node:fs"}, "src/b.ts": {"zod/v4", "../up"}} {
			id, err := b.UpsertFile(store.File{RepoID: repoA, Path: path, Lang: "typescript"})
			if err != nil {
				return err
			}
			imps := make([]store.Import, len(raws))
			for i, raw := range raws {
				imps[i] = store.Import{Raw: raw, Line: i + 1}
			}
			if err := b.ReplaceFileImports(id, imps); err != nil {
				return err
			}
		}
		id, err := b.UpsertFile(store.File{RepoID: repoB, Path: "main.go", Lang: "go"})
		if err != nil {
			return err
		}
		return b.ReplaceFileImports(id, []store.Import{{Raw: "fmt", Line: 3}})
	})
	sites, err := s.ImportSites(repoA)
	eq(t, "error", err, nil)
	eq(t, "repo a", sites, []store.ImportSite{{Path: "src/a.ts", Lang: "typescript", Raw: "node:fs"},
		{Path: "src/a.ts", Lang: "typescript", Raw: "zod"}, {Path: "src/b.ts", Lang: "typescript", Raw: "zod/v4"}})
	sites, err = s.ImportSites(repoB)
	eq(t, "error", err, nil)
	eq(t, "repo b", sites, []store.ImportSite{{Path: "main.go", Lang: "go", Raw: "fmt"}})
}

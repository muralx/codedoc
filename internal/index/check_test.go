package index_test

import (
	"path/filepath"
	"testing"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/store"
)

// wantCheck runs Check and fails unless ok accepts its answer.
func wantCheck(t *testing.T, s *store.Store, rep *config.Repo, what string, ok func(index.Freshness) bool) {
	t.Helper()
	f, err := index.Check(s, rep, index.Options{Workspace: "fixture"})
	if err != nil || !ok(f) {
		t.Fatalf("%s: %+v %v", what, f, err)
	}
}

// TestCheckReportsFreshnessWithoutWriting checks Check's freshness
// definition: the index reflects the working tree (an indexed dirty
// file is fresh), and files of another extractor version are not.
func TestCheckReportsFreshnessWithoutWriting(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	wantCheck(t, s, rep, "never indexed", func(f index.Freshness) bool { return !f.Fresh && f.Unindexed })
	indexRepo(t, s, rep)
	wantCheck(t, s, rep, "indexed", func(f index.Freshness) bool { return f.Fresh && !f.Unindexed && f.Changed == 0 })
	appendFile(t, rep.Path, "store/null.go", "\nfunc A() {}\n")
	edited := func(f index.Freshness) bool { return !f.Fresh && f.Changed == 1 && f.Dirty == 1 }
	wantCheck(t, s, rep, "edited", edited)
	wantCheck(t, s, rep, "Check wrote the edit", edited)
	ensure(t, s, rep, 10)
	wantCheck(t, s, rep, "an indexed dirty file is fresh", func(f index.Freshness) bool { return f.Fresh && f.Dirty == 1 })
	index.SetStoredExtractorVersion(t, s, rep.Path, "0")
	_, total := staleFiles(t, s, rep.Path)
	wantCheck(t, s, rep, "outdated extractor", func(f index.Freshness) bool { return !f.Fresh && f.Outdated == total })
	if f := ensure(t, s, rep, 1); f.Fresh || f.Outdated != total {
		t.Fatalf("Ensure over the limit: %+v", f)
	}
	gone := &config.Repo{Name: "gone", Path: filepath.Join(t.TempDir(), "gone")}
	wantCheck(t, s, gone, "missing", func(f index.Freshness) bool { return f.Missing && !f.Fresh })
}

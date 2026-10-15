package query_test

import (
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/query"
	"github.com/muralx/codedoc/internal/testutil"
)

// TestStaleNotesNameTheCause checks the note of an answer the freshness
// check could not bring up to date: a repo never indexed says so, and
// an index of an older extractor says how many files wait.
func TestStaleNotesNameTheCause(t *testing.T) {
	t.Setenv("CODEDOC_FRESH_MAX_FILES", "0")
	c := workspaceOf(t, "go-lib")
	c.Workspace.Repos = append(c.Workspace.Repos, &config.Repo{Name: "go-app", Path: testutil.CopyFixture(t, "go-app")})
	r, err := query.Find(c, "MemStore", query.FindOpts{})
	text := run(t, r, err)
	mustContain(t, text, "not indexed: go-app; run codedoc index")
	if strings.Contains(text, "index is stale") {
		t.Fatalf("a never-indexed repo reported stale:\n%s", text)
	}
	lib := c.Workspace.Repos[0]
	row, err := c.Store.RepoByPath(lib.Path)
	if err != nil {
		t.Fatal(err)
	}
	files, err := c.Store.FilesByRepo(row.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		f.ExtractedVersion = "0"
		if _, err := c.Store.UpsertFile(f); err != nil {
			t.Fatal(err)
		}
	}
	c.Fresh = nil
	r, err = query.Find(c, "MemStore", query.FindOpts{})
	mustContain(t, run(t, r, err), "extractor updated; ", " files to re-extract; run codedoc index")
}

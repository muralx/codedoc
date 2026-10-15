package query_test

import (
	"os"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/query"
)

func TestMissingRepoDoesNotFailOtherQueries(t *testing.T) {
	c := workspaceOf(t, "go-lib", "java-lib")
	gone := c.Workspace.Repos[1]
	away := gone.Path + ".away"
	if err := os.Rename(gone.Path, away); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Rename(away, gone.Path) })
	if !found(t, c, "MemStore", "store/store.go") {
		t.Fatal("healthy repo not answered")
	}
	resp, err := query.Find(c, "*", query.FindOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Header.Fresh || !strings.Contains(resp.Note, "directory missing, answered from the last index: java-lib") ||
		strings.Contains(resp.Note, "run codedoc index") {
		t.Fatalf("header %+v note %q", resp.Header, resp.Note)
	}
	if f := c.Fresh["java-lib"]; !f.Missing || f.Fresh {
		t.Fatalf("%+v", f)
	}
}

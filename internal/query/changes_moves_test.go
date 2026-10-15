package query_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/query"
)

func TestChangesFollowsRenamesAndMoves(t *testing.T) {
	c := workspaceOf(t, "go-lib")
	root := c.Workspace.Repos[0].Path
	base := gitIn(t, root, "2026-01-01T00:00:00Z", "rev-parse", "HEAD")
	gitIn(t, root, "2026-01-01T00:00:00Z", "mv", "store/null.go", "store/nothing.go")
	src, err := os.ReadFile(filepath.Join(root, "store", "store.go"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	start, end := strings.Index(s, "// Get implements Store."), strings.Index(s, "// Put implements Store.")
	get := strings.Replace(s[start:end], "return v, nil", "return v + \"\", nil", 1)
	if err := os.WriteFile(filepath.Join(root, "store", "store.go"), []byte(s[:start]+s[end:]), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "store", "get.go"), []byte("package store\n\n"+get), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := query.Changes(c, base)
	text := run(t, r, err)
	mustContain(t, text, "go-lib:store/nothing.go  renamed from store/null.go, 3 symbol changes",
		"store/nothing.go\n  NullStore  :4  type NullStore struct", "[moved] [doc:ok]",
		"MemStore.Get  store/get.go:4-10", "[moved] [body-changed]")
	if strings.Contains(text, "[removed]") || strings.Contains(text, "[added]") {
		t.Fatalf("a moved symbol is neither removed nor added:\n%s", text)
	}
}

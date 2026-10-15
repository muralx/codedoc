package store_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/store"
)

func TestUpsertRepoFollowsAMovedRepo(t *testing.T) {
	s := openTemp(t)
	dir := t.TempDir()
	old, now := filepath.Join(dir, "old"), filepath.Join(dir, "new")
	if err := os.Mkdir(now, 0o755); err != nil {
		t.Fatal(err)
	}
	id, err := s.UpsertRepo(store.Repo{Workspace: "w", Name: "api", Path: old})
	if err != nil {
		t.Fatal(err)
	}
	moved, err := s.UpsertRepo(store.Repo{Workspace: "w", Name: "api", Path: now})
	if err != nil || moved != id {
		t.Fatalf("a repo whose old path is gone keeps its row: %d %d %v", id, moved, err)
	}
	if r, err := s.RepoByPath(now); err != nil || r.ID != id {
		t.Fatalf("%+v %v", r, err)
	}
	other := filepath.Join(dir, "other")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err = s.UpsertRepo(store.Repo{Workspace: "w", Name: "api", Path: other})
	if err == nil || !strings.Contains(err.Error(), "codedoc index --rebuild") {
		t.Fatalf("both paths exist: %v", err)
	}
}

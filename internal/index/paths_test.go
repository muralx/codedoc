package index_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/store"
)

func TestPathsFollowsFilesAndDirectories(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	opts := index.Options{Workspace: "fixture", Exclude: []string{"**/skip/**"}}
	if _, err := index.Repo(s, rep, opts); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(rep.Path, "store")); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"extra/extra.go", "skip/skip.go"} {
		p := filepath.Join(rep.Path, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("package extra\n\nfunc Extra() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := index.Paths(t.Context(), s, rep, opts, []string{"store", "extra", "extra/extra.go", "skip/skip.go", "skip"}); err != nil {
		t.Fatal(err)
	}
	row, err := s.RepoByPath(rep.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"store/store.go", "store/doc.go", "skip/skip.go"} {
		if _, err := s.FileByPath(row.ID, rel); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("%s: %v", rel, err)
		}
	}
	fileRow(t, s, rep.Path, "extra/extra.go")
	fileRow(t, s, rep.Path, "service/service.go")
	if err := index.Paths(t.Context(), s, rep, opts, nil); err != nil {
		t.Fatal(err)
	}
}

func TestPathsRootRemovesEverythingGone(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	opts := index.Options{Workspace: "fixture"}
	if _, err := index.Repo(s, rep, opts); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(rep.Path, "service", "cache.go")); err != nil {
		t.Fatal(err)
	}
	if err := index.Paths(t.Context(), s, rep, opts, []string{"."}); err != nil {
		t.Fatal(err)
	}
	row, _ := s.RepoByPath(rep.Path)
	if _, err := s.FileByPath(row.ID, "service/cache.go"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
	fileRow(t, s, rep.Path, "service/service.go")
}

package store_test

import (
	"errors"
	"testing"

	"github.com/muralx/codedoc/internal/store"
)

func TestUpsertRepoAndFiles(t *testing.T) {
	s := openTemp(t)
	id, err := s.UpsertRepo(store.Repo{Workspace: "demo", Name: "a", Path: "/tmp/a", Branch: "main", Commit: "abc"})
	if err != nil {
		t.Fatal(err)
	}
	f := store.File{RepoID: id, Path: "x.go", Lang: "go", Size: 10, Mtime: 1, Hash: "h1", Lines: 2}
	fid, err := s.UpsertFile(f)
	if err != nil || fid == 0 {
		t.Fatalf("upsert file: %v", err)
	}
	got, err := s.FilesByRepo(id)
	if err != nil || len(got) != 1 || got[0].Hash != "h1" {
		t.Fatalf("files: %+v %v", got, err)
	}
	if err := s.DeleteFile(fid); err != nil {
		t.Fatal(err)
	}
	got, _ = s.FilesByRepo(id)
	if len(got) != 0 {
		t.Fatal("file not deleted")
	}
}

func TestUpsertRepoIsKeyedByPath(t *testing.T) {
	s := openTemp(t)
	id1, err := s.UpsertRepo(store.Repo{Workspace: "demo", Name: "a", Path: "/src/a", Branch: "main", Commit: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	id2, err := s.UpsertRepo(store.Repo{Workspace: "demo", Name: "renamed", Path: "/src/a", Branch: "dev", Commit: "c2"})
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 {
		t.Fatalf("ids differ: %d %d", id1, id2)
	}
	r, err := s.RepoByPath("/src/a")
	if err != nil {
		t.Fatal(err)
	}
	want := store.Repo{ID: id1, Workspace: "demo", Name: "renamed", Path: "/src/a", VCS: "git", Branch: "dev", Commit: "c2"}
	if r != want {
		t.Fatalf("got %+v want %+v", r, want)
	}
}

func TestUpsertRepoSharesWorkspaceRow(t *testing.T) {
	s := openTemp(t)
	a, err := s.UpsertRepo(store.Repo{Workspace: "demo", Name: "a", Path: "/src/a"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.UpsertRepo(store.Repo{Workspace: "demo", Name: "b", Path: "/src/b", VCS: "none"})
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("distinct repos share an id")
	}
	rb, err := s.RepoByPath("/src/b")
	if err != nil || rb.Workspace != "demo" || rb.VCS != "none" {
		t.Fatalf("repo b: %+v %v", rb, err)
	}
	// /src/a does not exist: the name follows the repo to its new path.
	if id, err := s.UpsertRepo(store.Repo{Workspace: "demo", Name: "a", Path: "/src/other"}); err != nil || id != a {
		t.Fatalf("moved repo: %d %v", id, err)
	}
}

func TestRepoByPathNotFound(t *testing.T) {
	s := openTemp(t)
	if _, err := s.RepoByPath("/nowhere"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestUpdateRepoHead(t *testing.T) {
	s := openTemp(t)
	id, err := s.UpsertRepo(store.Repo{Workspace: "demo", Name: "a", Path: "/src/a", Branch: "main", Commit: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateRepoHead(id, "feature", "c9"); err != nil {
		t.Fatal(err)
	}
	r, err := s.RepoByPath("/src/a")
	if err != nil || r.Branch != "feature" || r.Commit != "c9" {
		t.Fatalf("got %+v %v", r, err)
	}
	if err := s.UpdateRepoHead(id+100, "x", "y"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing repo: got %v", err)
	}
}

func TestUpsertFileUpdatesAllColumns(t *testing.T) {
	s := openTemp(t)
	repoID, err := s.UpsertRepo(store.Repo{Workspace: "demo", Name: "a", Path: "/src/a"})
	if err != nil {
		t.Fatal(err)
	}
	first := store.File{RepoID: repoID, Path: "x.go", Lang: "go", Size: 10, Mtime: 1, Hash: "h1", Lines: 2}
	id1, err := s.UpsertFile(first)
	if err != nil {
		t.Fatal(err)
	}
	second := store.File{RepoID: repoID, Path: "x.go", Lang: "go", Size: 20, Mtime: 5, Hash: "h2", Lines: 4,
		Generated: true, ParseErrors: 3, LastCommit: "c7", LastCommitAt: 1700000000}
	id2, err := s.UpsertFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 {
		t.Fatalf("upsert changed id: %d -> %d", id1, id2)
	}
	got, err := s.FileByPath(repoID, "x.go")
	if err != nil {
		t.Fatal(err)
	}
	second.ID = id1
	if got != second {
		t.Fatalf("got %+v want %+v", got, second)
	}
}

func TestFileByPathNotFound(t *testing.T) {
	s := openTemp(t)
	repoID, err := s.UpsertRepo(store.Repo{Workspace: "demo", Name: "a", Path: "/src/a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.FileByPath(repoID, "missing.go"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestFilesByRepoSortedAndScoped(t *testing.T) {
	s := openTemp(t)
	a, _ := s.UpsertRepo(store.Repo{Workspace: "demo", Name: "a", Path: "/src/a"})
	b, _ := s.UpsertRepo(store.Repo{Workspace: "demo", Name: "b", Path: "/src/b"})
	for _, f := range []store.File{
		{RepoID: a, Path: "z.go", Hash: "1"},
		{RepoID: a, Path: "m/a.go", Hash: "2"},
		{RepoID: b, Path: "other.go", Hash: "3"},
	} {
		if _, err := s.UpsertFile(f); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.FilesByRepo(a)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Path != "m/a.go" || got[1].Path != "z.go" {
		t.Fatalf("got %+v", got)
	}
}

func TestDeleteFileMissing(t *testing.T) {
	s := openTemp(t)
	if err := s.DeleteFile(42); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestUpsertFileRequiresExistingRepo(t *testing.T) {
	s := openTemp(t)
	if _, err := s.UpsertFile(store.File{RepoID: 999, Path: "x.go", Hash: "h"}); err == nil {
		t.Fatal("expected foreign key error")
	}
}

func TestSetRepoIndexedAt(t *testing.T) {
	s := openTemp(t)
	id, _ := s.UpsertRepo(store.Repo{Workspace: "demo", Name: "a", Path: "/src/a"})
	if err := s.SetRepoIndexedAt(id, 1700000000); err != nil {
		t.Fatal(err)
	}
	r, _ := s.RepoByPath("/src/a")
	if r.IndexedAt != 1700000000 {
		t.Fatalf("%+v", r)
	}
	if _, err := s.UpsertRepo(store.Repo{Workspace: "demo", Name: "a", Path: "/src/a", Commit: "c"}); err != nil {
		t.Fatal(err)
	}
	if r, _ := s.RepoByPath("/src/a"); r.IndexedAt != 1700000000 {
		t.Fatal("UpsertRepo must keep indexed_at")
	}
	if err := s.SetRepoIndexedAt(999, 1); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestRepoStatsCountsFilesAndSymbols(t *testing.T) {
	s := openTemp(t)
	id, _ := s.UpsertRepo(store.Repo{Workspace: "demo", Name: "a", Path: "/src/a"})
	for _, p := range []string{"a.go", "b.go"} {
		if _, err := s.UpsertFile(store.File{RepoID: id, Path: p, Hash: "h"}); err != nil {
			t.Fatal(err)
		}
	}
	files, symbols, err := s.RepoStats(id)
	if err != nil || files != 2 || symbols != 0 {
		t.Fatalf("files %d symbols %d err %v", files, symbols, err)
	}
}

func TestBatchCommitsAllWrites(t *testing.T) {
	s := openTemp(t)
	id, err := s.UpsertRepo(store.Repo{Workspace: "demo", Name: "a", Path: "/src/a"})
	if err != nil {
		t.Fatal(err)
	}
	old, err := s.UpsertFile(store.File{RepoID: id, Path: "old.go", Hash: "h0"})
	if err != nil {
		t.Fatal(err)
	}
	err = s.Batch(func(b *store.Batch) error {
		for _, p := range []string{"x.go", "y.go"} {
			if _, err := b.UpsertFile(store.File{RepoID: id, Path: p, Hash: "h"}); err != nil {
				return err
			}
		}
		_, err := b.DeleteFiles([]int64{old})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.FilesByRepo(id)
	if len(got) != 2 || got[0].Path != "x.go" || got[1].Path != "y.go" {
		t.Fatalf("%+v", got)
	}
}

func TestBatchRollsBackOnError(t *testing.T) {
	s := openTemp(t)
	id, err := s.UpsertRepo(store.Repo{Workspace: "demo", Name: "a", Path: "/src/a"})
	if err != nil {
		t.Fatal(err)
	}
	err = s.Batch(func(b *store.Batch) error {
		if _, err := b.UpsertFile(store.File{RepoID: id, Path: "x.go", Hash: "h"}); err != nil {
			return err
		}
		_, err := b.DeleteFiles([]int64{12345})
		return err
	})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if got, _ := s.FilesByRepo(id); len(got) != 0 {
		t.Fatalf("batch not rolled back: %+v", got)
	}
}

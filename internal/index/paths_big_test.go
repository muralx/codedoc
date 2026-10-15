package index_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/store"
)

// slow scales a time limit for the race detector, which slows SQLite
// calls several times over.
func slow(d time.Duration) time.Duration {
	if raceEnabled {
		return 4 * d
	}
	return d
}

// bigTree stores rows for files big/dNNN/fNNN.txt (no language, so no
// module rows are needed), as an index of a
// 50,000-file tree that was then deleted, and returns their paths.
func bigTree(t *testing.T, s *store.Store, rep *config.Repo, n int) []string {
	t.Helper()
	row, err := s.RepoByPath(rep.Path)
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, n)
	err = s.Batch(func(b *store.Batch) error {
		for i := range paths {
			paths[i] = fmt.Sprintf("big/d%03d/f%03d.txt", i/1000, i%1000)
			if _, err := b.UpsertFile(store.File{RepoID: row.ID, Path: paths[i], Hash: "h"}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

func storedFiles(t *testing.T, s *store.Store, root string) int {
	t.Helper()
	row, err := s.RepoByPath(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := s.FilesByRepo(row.ID)
	if err != nil {
		t.Fatal(err)
	}
	return len(files)
}

func TestPathsRemovesALargeDeletedTreeQuickly(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	opts := index.Options{Workspace: "fixture"}
	if _, err := index.Repo(s, rep, opts); err != nil {
		t.Fatal(err)
	}
	kept := storedFiles(t, s, rep.Path)
	paths := bigTree(t, s, rep, 50000)
	start := time.Now()
	if err := index.Paths(t.Context(), s, rep, opts, append([]string{"big"}, paths...)); err != nil {
		t.Fatal(err)
	}
	// About 1 s here (3 s under the race detector): the limit leaves six
	// times that for a loaded machine. What keeps the cost per file down,
	// a few statements per batch of files, has its own test in the store
	// (TestDeleteFilesPreparesItsStatementsOncePerShape).
	d := time.Since(start)
	t.Logf("removing 50,000 files took %s", d)
	if d > slow(6*time.Second) {
		t.Errorf("removing 50,000 files took %s", d)
	}
	if n := storedFiles(t, s, rep.Path); n != kept {
		t.Fatalf("%d files stored, want %d", n, kept)
	}
}

func TestPathsStopsBetweenRoundsWhenCancelled(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	opts := index.Options{Workspace: "fixture"}
	if _, err := index.Repo(s, rep, opts); err != nil {
		t.Fatal(err)
	}
	kept := storedFiles(t, s, rep.Path)
	paths := bigTree(t, s, rep, 50000)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- index.Paths(ctx, s, rep, opts, paths) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	start := time.Now()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want context.Canceled", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Paths ignored the cancellation")
	}
	d := time.Since(start)
	t.Logf("stopping took %s", d)
	if d > slow(5*time.Second) {
		t.Errorf("stopping took %s", d)
	}
	if n := storedFiles(t, s, rep.Path); n <= kept {
		t.Fatalf("cancelled at once or not at all: %d files left", n)
	}
	if _, err := index.Ensure(s, rep, index.Options{Workspace: "fixture", MaxFiles: 200}); err != nil {
		t.Fatal(err)
	}
	if n := storedFiles(t, s, rep.Path); n != kept {
		t.Fatalf("the next freshness pass left %d files, want %d", n, kept)
	}
}

func TestEnsureKeepsTheIndexOfAMissingRepo(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	if _, err := index.Repo(s, rep, index.Options{Workspace: "fixture"}); err != nil {
		t.Fatal(err)
	}
	kept := storedFiles(t, s, rep.Path)
	moved := rep.Path + ".away"
	if err := os.Rename(rep.Path, moved); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Rename(moved, rep.Path) })
	f, err := index.Ensure(s, rep, index.Options{Workspace: "fixture", MaxFiles: 200})
	if err != nil || !f.Missing || f.Fresh {
		t.Fatalf("%+v %v", f, err)
	}
	if n := storedFiles(t, s, rep.Path); n != kept {
		t.Fatalf("index of a missing repo changed: %d files, want %d", n, kept)
	}
}

func TestEnsureContextWritesLargeChangesInRounds(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	opts := index.Options{Workspace: "fixture", MaxFiles: 1 << 30}
	if _, err := index.Repo(s, rep, opts); err != nil {
		t.Fatal(err)
	}
	kept := storedFiles(t, s, rep.Path)
	bigTree(t, s, rep, 6000)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := index.EnsureContext(ctx, s, rep, opts); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
	if f, err := index.EnsureContext(t.Context(), s, rep, opts); err != nil || !f.Fresh {
		t.Fatalf("%+v %v", f, err)
	}
	if n := storedFiles(t, s, rep.Path); n != kept {
		t.Fatalf("%d files, want %d", n, kept)
	}
	if _, err := os.Stat(filepath.Join(rep.Path, "big")); !os.IsNotExist(err) {
		t.Fatal("fixture has big/")
	}
}

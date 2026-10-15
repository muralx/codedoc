package index_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/store"
)

func assertSymbolMatches(t *testing.T, s *store.Store, match string, want int) {
	t.Helper()
	ids, err := s.SymbolMatches(match, store.SearchFilter{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != want {
		t.Fatalf("SymbolMatches(%q) = %v, want %d ids", match, ids, want)
	}
}

// A first index writes in bulk FTS mode and rebuilds the text index at
// the end; later syncs keep it current through the triggers.
func TestFirstIndexRebuildsTextIndexInBulk(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	opts := index.WithBulk(index.Options{Workspace: "fixture"}, 1, nil)
	if _, err := index.Repo(s, rep, opts); err != nil {
		t.Fatal(err)
	}
	assertSymbolMatches(t, s, "name:Lookup", 1)
	if _, ok, err := s.Meta("fts_rebuild_pending"); err != nil || ok {
		t.Fatalf("fts_rebuild_pending left set: %v %v", ok, err)
	}
	p := filepath.Join(rep.Path, "service", "extra.go")
	if err := os.WriteFile(p, []byte("package service\n\nfunc Extra() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Repo(s, rep, opts); err != nil {
		t.Fatal(err)
	}
	assertSymbolMatches(t, s, "name:Extra", 1)
}

// A sync that dies after its bulk writes, before the text index is
// rebuilt, leaves it to the next writer or Open.
func TestInterruptedBulkIndexIsRebuiltLater(t *testing.T) {
	crash := errors.New("crash")
	path := filepath.Join(t.TempDir(), "ws.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	rep := repoFixture(t, "go-lib")
	crashing := index.WithBulk(index.Options{Workspace: "fixture"}, 1, func(*store.Store) error { return crash })
	if _, err := index.Repo(s, rep, crashing); !errors.Is(err, crash) {
		t.Fatalf("Repo: %v, want the crash", err)
	}
	opts := index.WithBulk(index.Options{Workspace: "fixture"}, 1, nil)
	assertSymbolMatches(t, s, "name:Lookup", 0)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s, err = store.Open(path); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	assertSymbolMatches(t, s, "name:Lookup", 1)
	if _, err := index.Repo(s, rep, opts); err != nil {
		t.Fatal(err)
	}
	assertSymbolMatches(t, s, "name:Lookup", 1)
}

// A small sync into a database holding more files than it writes keeps
// the triggers.
func TestSmallSyncKeepsTriggers(t *testing.T) {
	s := openTemp(t)
	opts := index.WithBulk(index.Options{Workspace: "fixture"}, 1, nil)
	if _, err := index.Repo(s, repoFixture(t, "java-lib"), opts); err != nil {
		t.Fatal(err)
	}
	failing := index.WithBulk(opts, 1, func(*store.Store) error { return errors.New("bulk mode used") })
	if _, err := index.Repo(s, repoFixture(t, "go-app"), failing); err != nil {
		t.Fatal(err)
	}
	assertSymbolMatches(t, s, "name:main", 1)
}

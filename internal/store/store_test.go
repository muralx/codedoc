package store_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/muralx/codedoc/internal/store"
)

func openTemp(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "ws.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOpenCreatesSchemaAndIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ws.db")
	for i := 0; i < 2; i++ {
		s, err := store.Open(path)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		v, err := s.SchemaVersion()
		if err != nil || v < 1 {
			t.Fatalf("version %d err %v", v, err)
		}
		_ = s.Close()
	}
}

func TestOpenCreatesParentDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "ws.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("database file missing: %v", err)
	}
}

func TestOpenFailsWhenParentIsAFile(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Open(filepath.Join(blocker, "ws.db")); err == nil {
		t.Fatal("expected error")
	}
}

func TestOpenFailsOnCorruptDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ws.db")
	if err := os.WriteFile(path, []byte("this is not a sqlite database, just text padding"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Open(path); err == nil {
		t.Fatal("expected error")
	}
}

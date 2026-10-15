package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/muralx/codedoc/internal/daemon"
	"github.com/muralx/codedoc/internal/store"
)

func TestRebuildRefusesWhileTheDaemonRuns(t *testing.T) {
	cfgDir, _ := setupWorkspace(t)
	if _, err := runCLI(t, "index", "--workspace", "fx"); err != nil {
		t.Fatal(err)
	}
	release, err := daemon.LockInstance(filepath.Join(cfgDir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = runCLI(t, "index", "--workspace", "fx", "--rebuild")
	if err == nil || !strings.Contains(err.Error(), "systemctl --user stop codedoc") {
		t.Fatalf("rebuild under a running daemon: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfgDir, "data", "fx.db")); err != nil {
		t.Fatalf("the refused rebuild deleted the database: %v", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if _, err := runCLI(t, "index", "--workspace", "fx", "--rebuild"); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveDBWaitsForTheWriteLock(t *testing.T) {
	db := filepath.Join(t.TempDir(), "fx.db")
	if err := os.WriteFile(db, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	unlock, err := store.LockPath(db)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- removeDB(db) }()
	select {
	case err := <-done:
		t.Fatalf("removeDB did not wait for the writer: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	if _, err := os.Stat(db); err != nil {
		t.Fatalf("deleted under a writer: %v", err)
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(db); !os.IsNotExist(err) {
		t.Fatalf("database kept: %v", err)
	}
}

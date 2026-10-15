package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/session"
)

// lockWorkspace writes a config with workspace fx of repos a and b, points
// CODEDOC_CONFIG and CODEDOC_DATA at it, moves into repo a and returns the
// data dir.
func lockWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, r := range []string{"a", "b"} {
		if err := os.MkdirAll(filepath.Join(root, r), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(cfg, []byte("workspaces:\n  fx:\n    repos: [{path: a}, {path: b}]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(root, "data")
	t.Setenv("CODEDOC_CONFIG", cfg)
	t.Setenv("CODEDOC_DATA", data)
	t.Setenv(session.HeadlessEnv, "")
	t.Chdir(filepath.Join(root, "a"))
	return data
}

func held(t *testing.T, data, repo string) bool {
	t.Helper()
	h, err := session.Held(data, "fx", repo)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestServeHoldsSessionLocksUntilExit(t *testing.T) {
	data := lockWorkspace(t)
	if held(t, data, "a") {
		t.Fatal("held before the server started")
	}
	_, stop := connect(t, Options{Exec: (&recorder{}).exec, Log: &lines{}})
	if !held(t, data, "a") || !held(t, data, "b") {
		t.Fatal("locks not taken")
	}
	stop()
	if held(t, data, "a") || held(t, data, "b") {
		t.Fatal("locks not released")
	}
}

func TestHeadlessTakesNoLocks(t *testing.T) {
	data := lockWorkspace(t)
	t.Setenv(session.HeadlessEnv, "1")
	connect(t, Options{Exec: (&recorder{}).exec, Log: &lines{}})
	if held(t, data, "a") || held(t, data, "b") {
		t.Fatal("headless server took a lock")
	}
	if _, err := os.Stat(filepath.Join(data, "fx.locks")); !os.IsNotExist(err) {
		t.Fatalf("lock dir created: %v", err)
	}
}

func TestLockHeldByAnotherSessionIsNotFatal(t *testing.T) {
	data := lockWorkspace(t)
	f, err := session.Lock(data, "fx", "b")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	log := &lines{}
	_, stop := connect(t, Options{Exec: (&recorder{}).exec, Log: log})
	if !held(t, data, "a") || !strings.Contains(log.String(), "session lock fx/b not taken") {
		t.Fatalf("log: %s", log)
	}
	stop()
	if !held(t, data, "b") {
		t.Fatal("other session's lock released")
	}
}

func TestNoWorkspaceTakesNoLocks(t *testing.T) {
	data := lockWorkspace(t)
	t.Chdir(t.TempDir())
	log := &lines{}
	connect(t, Options{Exec: (&recorder{}).exec, Log: log})
	if held(t, data, "a") || !strings.Contains(log.String(), "tools need the workspace input") {
		t.Fatalf("log: %s", log)
	}
	t.Setenv("CODEDOC_CONFIG", filepath.Join(t.TempDir(), "missing.yaml"))
	log = &lines{}
	connect(t, Options{Exec: (&recorder{}).exec, Log: log})
	if !strings.Contains(log.String(), "no session locks") {
		t.Fatalf("log: %s", log)
	}
}

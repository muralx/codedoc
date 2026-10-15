package session_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/muralx/codedoc/internal/session"
)

func TestHeadless(t *testing.T) {
	t.Setenv(session.HeadlessEnv, "")
	if session.Headless() {
		t.Fatal("headless without the variable")
	}
	t.Setenv(session.HeadlessEnv, "1")
	if !session.Headless() {
		t.Fatal("not headless")
	}
}

func TestLockThenHeld(t *testing.T) {
	data := t.TempDir()
	f, err := session.Lock(data, "fx", "a")
	if err != nil {
		t.Fatal(err)
	}
	if h, err := session.Held(data, "fx", "a"); !h || err != nil {
		t.Fatalf("%v %v", h, err)
	}
	if _, err := session.Lock(data, "fx", "a"); err == nil {
		t.Fatal("second lock taken")
	}
	_ = f.Close()
	if h, err := session.Held(data, "fx", "a"); h || err != nil {
		t.Fatalf("%v %v", h, err)
	}
}

func TestLockAndHeldErrors(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Lock(file, "fx", "a"); err == nil {
		t.Fatal("lock under a file")
	}
	if err := os.MkdirAll(session.LockPath(root, "fx", "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Lock(root, "fx", "dir"); err == nil {
		t.Fatal("lock on a directory")
	}
	if _, err := session.Held(root, "fx", "dir"); err == nil {
		t.Fatal("held on a directory")
	}
	if h, err := session.Held(root, "fx", "never"); h || err != nil {
		t.Fatalf("%v %v", h, err)
	}
}

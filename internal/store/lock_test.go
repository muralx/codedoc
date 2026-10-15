package store_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/muralx/codedoc/internal/store"
)

// Two handles on one database exclude each other through the write lock,
// as two processes would.
func TestWriteLockSerializesHandles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ws.db")
	a, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	b, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Close() }()
	unlock, err := a.WriteLock()
	if err != nil {
		t.Fatal(err)
	}
	var acquired atomic.Bool
	done := make(chan error, 1)
	go func() {
		unlockB, err := b.WriteLock()
		if err == nil {
			acquired.Store(true)
			err = unlockB()
		}
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	if acquired.Load() {
		t.Fatal("second handle took the lock while the first held it")
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil || !acquired.Load() {
		t.Fatalf("second handle: %v", err)
	}
}

func TestWriteLockFailsWhenLockFileCannotBeCreated(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "ws.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if err := mkdirLockPath(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WriteLock(); err == nil {
		t.Fatal("expected an error when the lock path is a directory")
	}
}

func mkdirLockPath(dir string) error { return os.Mkdir(filepath.Join(dir, "ws.db.lock"), 0o755) }

func TestWriteLockTimesOutNamingTheHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ws.db")
	unlock, err := store.LockPath(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unlock() }()
	t.Setenv(store.LockTimeoutEnv, "50ms")
	start := time.Now()
	_, err = store.LockPath(path)
	if !errors.Is(err, store.ErrLockTimeout) || time.Since(start) < 50*time.Millisecond {
		t.Fatalf("%v after %v", err, time.Since(start))
	}
	want := fmt.Sprintf("held by pid %d: ", os.Getpid())
	if !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), store.LockTimeoutEnv) {
		t.Fatalf("%v", err)
	}
}

func TestLockTimeout(t *testing.T) {
	for v, want := range map[string]time.Duration{"": store.DefaultLockTimeout, "5": 5 * time.Second,
		"1m30s": 90 * time.Second, "0": 0} {
		t.Setenv(store.LockTimeoutEnv, v)
		if got, err := store.LockTimeout(); err != nil || got != want {
			t.Errorf("%q: %v %v", v, got, err)
		}
	}
	for _, v := range []string{"soon", "-5s"} {
		t.Setenv(store.LockTimeoutEnv, v)
		if _, err := store.LockTimeout(); err == nil {
			t.Errorf("%q accepted", v)
		}
		if _, err := store.LockPath(filepath.Join(t.TempDir(), "x.db")); err == nil {
			t.Errorf("%q: lock taken", v)
		}
	}
}

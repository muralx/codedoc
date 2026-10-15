package daemon

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultLogPath(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/state")
	if got := DefaultLogPath(); got != "/state/codedoc/daemon.log" {
		t.Fatalf("got %s", got)
	}
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/home/u")
	if got := DefaultLogPath(); got != "/home/u/.local/state/codedoc/daemon.log" {
		t.Fatalf("got %s", got)
	}
	t.Setenv("XDG_STATE_HOME", "relative/state")
	if got := DefaultLogPath(); got != "/home/u/.local/state/codedoc/daemon.log" {
		t.Fatalf("relative XDG_STATE_HOME used: %s", got)
	}
}

func TestLoggerWritesStderrAndFileAndRotates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "daemon.log")
	var stderr bytes.Buffer
	l, err := NewLogger(&stderr, path)
	if err != nil {
		t.Fatal(err)
	}
	l.max = 100
	for i := range 5 {
		l.Printf("line %d %s", i, strings.Repeat("x", 30))
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if strings.Count(stderr.String(), "\n") != 5 {
		t.Fatalf("stderr: %q", stderr.String())
	}
	cur, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	old, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cur), "line 4") || !strings.Contains(string(old), "line 3") {
		t.Fatalf("current %q, rotated %q", cur, old)
	}
	if len(cur) > 100 || len(old) > 100 {
		t.Fatalf("sizes %d, %d exceed the limit", len(cur), len(old))
	}
}

func TestLoggerAppendsAndStderrOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "d.log")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := NewLogger(&bytes.Buffer{}, path)
	if err != nil {
		t.Fatal(err)
	}
	l.Printf("new")
	_ = l.Close()
	raw, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(raw), "old\n") || !strings.Contains(string(raw), "new") {
		t.Fatalf("file: %q", raw)
	}
	var stderr bytes.Buffer
	only, err := NewLogger(&stderr, "")
	if err != nil {
		t.Fatal(err)
	}
	only.Printf("hello %d", 1)
	if !strings.Contains(stderr.String(), "hello 1") || only.Close() != nil {
		t.Fatalf("stderr: %q", stderr.String())
	}
}

func TestLoggerRotationFailureReported(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "d.log")
	var stderr bytes.Buffer
	l, err := NewLogger(&stderr, path)
	if err != nil {
		t.Fatal(err)
	}
	l.max = 10
	l.Printf("first line")
	if err := os.Mkdir(path+".1", 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, path+".1", "occupied", "x")
	l.Printf("second line")
	if !strings.Contains(stderr.String(), "log rotation failed") {
		t.Fatalf("stderr: %q", stderr.String())
	}
	_ = l.Close()
}

func TestNewLoggerFailsOnUnwritableDir(t *testing.T) {
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewLogger(&bytes.Buffer{}, filepath.Join(file, "d.log")); err == nil {
		t.Fatal("expected error")
	}
}

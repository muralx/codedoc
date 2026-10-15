package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/mattn/go-sqlite3"
	"github.com/spf13/cobra"

	"github.com/muralx/codedoc/internal/query"
)

func TestExitCode(t *testing.T) {
	cases := []struct {
		err    error
		strict bool
		want   int
	}{
		{ErrNotFound, false, exitFailed},
		{fmt.Errorf("symbol x: %w", ErrNotFound), true, exitFailed},
		{fmt.Errorf("name y: %w", ErrAmbiguous), true, exitAmbiguous},
		{fmt.Errorf("name y: %w", ErrAmbiguous), false, exitFailed},
		{&query.DocSetError{Addr: "y", Err: ErrAmbiguous}, false, exitFailed},
		{errors.New("boom"), false, exitFailed},
		{fmt.Errorf("3 of 4 docs failed: %w", query.ErrReported), false, exitFailed},
		{usage("feedback needs a text"), false, exitUsage},
		{usageError{errors.New("unknown flag: --nope")}, false, exitUsage},
		{errors.New("--depth must be at least 1"), false, exitUsage},
		{errors.New(`unknown --sort "x"; valid: name`), false, exitUsage},
		{fmt.Errorf("lines: %w", query.ErrUsage), false, exitUsage},
		{errors.New("--rebuild: the codedoc daemon serves fx"), false, exitFailed},
		{&fs.PathError{Op: "open", Path: "/x", Err: syscall.EACCES}, false, exitInternal},
		{fmt.Errorf("config: %w", &fs.PathError{Op: "open", Path: "/x", Err: fs.ErrNotExist}), false, exitFailed},
		{fmt.Errorf("query: %w", sqlite3.Error{Code: sqlite3.ErrCorrupt}), false, exitInternal},
		{&os.SyscallError{Syscall: "read", Err: syscall.EIO}, false, exitInternal},
	}
	for _, c := range cases {
		if got := exitCode(c.err, c.strict); got != c.want {
			t.Errorf("exitCode(%v, strict %v) = %d, want %d", c.err, c.strict, got, c.want)
		}
	}
}

// exitOf runs the command line as main does and returns what it printed
// and its exit status.
func exitOf(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errs bytes.Buffer
	root := newRootCmd()
	root.SetOut(&out)
	root.SetErr(&errs)
	root.SetArgs(args)
	code = execute(root, &out, &errs)
	return out.String(), errs.String(), code
}

// The command line itself being wrong is exit status 2, whatever part
// of it: cobra's errors before the command runs and the values a
// command checks itself.
func TestUsageErrorsExitTwo(t *testing.T) {
	setupWorkspace(t)
	for _, args := range [][]string{
		{"nosuchcommand"},
		{"symbol"},
		{"symbol", "A", "--nope"},
		{"symbol", "A", "--budget", "many"},
		{"symbol", "A", "--no-body", "--body"},
		{"symbol", "A", "--context", "-1", "--workspace", "fx"},
		{"callers", "A", "--depth", "0", "--workspace", "fx"},
		{"callers", "A", "--group-by", "dir", "--workspace", "fx"},
		{"outline", "go-lib:store", "--sort", "color", "--workspace", "fx"},
		{"lines", "go-lib:store/store.go", "x-y", "--workspace", "fx"},
		{"find", "A", "--offset", "-1", "--workspace", "fx"},
		{"gaps", "--kind", "bogus", "--workspace", "fx"},
		{"feedback"},
		{"doc", "set"},
	} {
		stdout, stderr, code := exitOf(t, args...)
		if code != exitUsage || stdout != "" || !strings.HasPrefix(stderr, "codedoc: ") || strings.Count(stderr, "\n") != 1 {
			t.Errorf("%v: exit %d, stdout %q, stderr %q", args, code, stdout, stderr)
		}
	}
}

// A panic is an internal error: one line with the value, the stack, and
// exit status 4.
func TestAPanicExitsFour(t *testing.T) {
	root := newRootCmd()
	root.AddCommand(&cobra.Command{Use: "explode", RunE: func(*cobra.Command, []string) error { panic("boom") }})
	root.SetArgs([]string{"explode"})
	var out, errs bytes.Buffer
	if code := execute(root, &out, &errs); code != exitInternal || !strings.HasPrefix(errs.String(), "codedoc: internal error: boom\n") {
		t.Fatalf("exit %d, stderr %q", code, errs.String())
	}
}

// A database that cannot be read is an internal error.
func TestAnUnreadableDatabaseExitsFour(t *testing.T) {
	cfgDir, _ := setupWorkspace(t)
	if err := os.MkdirAll(filepath.Join(cfgDir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "data", "fx.db"), []byte("this is not a database, nor will it be one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, stderr, code := exitOf(t, "find", "A", "--workspace", "fx"); code != exitInternal {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
}

func TestReportErrorAddsAJSONObjectInJSONMode(t *testing.T) {
	setupWorkspace(t)
	root := newRootCmd()
	root.SetArgs([]string{"symbol", "Nope", "--workspace", "fx", "--format", "json", "--strict"})
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	cmd, err := root.ExecuteC()
	if err == nil {
		t.Fatal("no error")
	}
	reportError(&stdout, &stderr, cmd, err)
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if last := lines[len(lines)-1]; last != `{"error":"symbol Nope: not found","code":1}` {
		t.Fatalf("%q", stdout.String())
	}
	if stderr.String() != "codedoc: symbol Nope: not found\n" {
		t.Fatalf("%q", stderr.String())
	}
	stdout.Reset()
	reportError(&stdout, &stderr, newRootCmd(), err)
	if stdout.Len() != 0 {
		t.Fatalf("text mode wrote %q", stdout.String())
	}
}

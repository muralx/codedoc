package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/muralx/codedoc/internal/daemon"
)

// startDaemon runs `codedoc daemon` in process until the test ends and
// waits until it reports go-lib fresh.
func startDaemon(t *testing.T, args ...string) *bytes.Buffer {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	var log bytes.Buffer
	done := make(chan error, 1)
	root := newRootCmd()
	root.SetArgs(append([]string{"daemon"}, args...))
	root.SetOut(&log)
	root.SetErr(&log)
	go func() { done <- root.ExecuteContext(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("daemon: %v", err)
			}
		case <-time.After(20 * time.Second):
			t.Error("daemon did not stop")
		}
	})
	waitFor(t, "the daemon", func() bool { return daemonReports(t, "go-lib", func(r daemon.RepoStatus) bool { return r.Fresh }) })
	return &log
}

// daemonReports reports whether the daemon's status of repo name of fx
// satisfies cond.
func daemonReports(t *testing.T, name string, cond func(daemon.RepoStatus) bool) bool {
	t.Helper()
	st, err := daemon.QueryStatus(daemon.SocketPath())
	if err != nil {
		return false
	}
	for _, ws := range st.Workspaces {
		for _, r := range ws.Repos {
			if ws.Name == "fx" && r.Name == name {
				return cond(r)
			}
		}
	}
	return false
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDaemonCommandKeepsQueriesFresh(t *testing.T) {
	_, goLib := setupWorkspace(t)
	out := cli(t, "status", "--workspace", "fx")
	if !strings.HasSuffix(out, "daemon: not running\n") {
		t.Fatal(out)
	}
	startDaemon(t, "--workspace", "fx")
	out = cli(t, "status", "--workspace", "fx")
	if !strings.HasSuffix(out, "daemon: running\n") ||
		!regexp.MustCompile(`(?m)^go-lib  main@[0-9a-f]{7} files:8 .* scip:never queue:0 upkeep:off$`).MatchString(out) {
		t.Fatal(out)
	}
	src := "package store\n\n// Watched is new.\nfunc Watched() {}\n"
	if err := os.WriteFile(filepath.Join(goLib, "store", "watched.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the edit in query answers", func() bool {
		return strings.Contains(cli(t, "find", "Watched", "--workspace", "fx"), "store/watched.go")
	})
}

func TestDaemonCommandFailures(t *testing.T) {
	setupWorkspace(t)
	if _, err := runCLI(t, "daemon", "--workspace", "nope"); err == nil {
		t.Fatal("unknown workspace accepted")
	}
	t.Setenv("CODEDOC_CONFIG", filepath.Join(t.TempDir(), "missing.yaml"))
	if _, err := runCLI(t, "daemon"); err == nil {
		t.Fatal("missing config accepted")
	}
}

func TestDaemonText(t *testing.T) {
	at := time.Date(2026, 10, 9, 2, 0, 0, 0, time.Local)
	got := daemonText(daemon.RepoStatus{Queue: 2, NextSCIP: at, NextAutodoc: at, LastError: "index: boom\nstack",
		Degraded: "watch error: x"})
	if got != " queue:2 degraded: watch error: x next-scip:2026-10-09 02:00:00 next-autodoc:2026-10-09 02:00:00 upkeep:off last-error: index: boom" {
		t.Fatal(got)
	}
}

func TestUpkeepText(t *testing.T) {
	at := time.Date(2026, 10, 9, 2, 0, 0, 0, time.Local)
	for want, u := range map[string]daemon.UpkeepStatus{
		" queue:0 upkeep:on pending:0":                                        {Enabled: true},
		" queue:0 upkeep:on (uncommitted changes) pending:3":                  {Enabled: true, Waiting: "uncommitted changes", Pending: 3},
		" queue:0 upkeep:running pending:3":                                   {Enabled: true, Running: true, Pending: 3},
		" queue:0 upkeep:on (daily cap reached) pending:1":                    {Enabled: true, CapReached: true, Waiting: "daily cap reached", Pending: 1, SpentToday: 5},
		" queue:0 upkeep:on pending:0 last-upkeep:2026-10-09 02:00:00":        {Enabled: true, LastRun: at},
		" queue:0 upkeep:on pending:2 upkeep-error: run undone: a.go changed": {Enabled: true, Pending: 2, LastError: "run undone: a.go changed\nmore"},
	} {
		if got := daemonText(daemon.RepoStatus{Upkeep: u}); got != want {
			t.Errorf("got  %q\nwant %q", got, want)
		}
	}
}

// enableUpkeep turns upkeep on in the workspace setupWorkspace wrote.
func enableUpkeep(t *testing.T) {
	t.Helper()
	path := os.Getenv("CODEDOC_CONFIG")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(body, "    upkeep: {enabled: true}\n"...), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestStatusSaysUpkeepNeedsTheDaemon(t *testing.T) {
	setupWorkspace(t)
	enableUpkeep(t)
	cli(t, "index", "--workspace", "fx")
	out := cli(t, "status", "--workspace", "fx")
	if !regexp.MustCompile(`(?m)^go-lib  main@[0-9a-f]{7} files:8 .* scip:never upkeep:needs-daemon$`).MatchString(out) ||
		!strings.HasSuffix(out, "daemon: not running; upkeep needs the daemon (codedoc daemon): nothing is documented without it\n") {
		t.Fatal(out)
	}
	out = cli(t, "status", "--workspace", "fx", "--repo", "go-lib", "--format", "json")
	if !strings.Contains(out, `"upkeep":{"enabled":true,"needs_daemon":true}`) {
		t.Fatal(out)
	}
}

func TestStatusShowsTheDaemonsUpkeepState(t *testing.T) {
	setupWorkspace(t)
	enableUpkeep(t)
	startDaemon(t, "--workspace", "fx")
	waitFor(t, "upkeep in the daemon's status", func() bool {
		return daemonReports(t, "go-lib", func(r daemon.RepoStatus) bool { return r.Upkeep.Enabled })
	})
	out := cli(t, "status", "--workspace", "fx")
	if !regexp.MustCompile(`(?m)^go-lib  main@[0-9a-f]{7} .* queue:0 upkeep:on \(waiting for quiet\) pending:0$`).MatchString(out) ||
		!strings.HasSuffix(out, "daemon: running\n") {
		t.Fatal(out)
	}
	out = cli(t, "status", "--workspace", "fx", "--repo", "go-lib", "--format", "json")
	if !strings.Contains(out, `"upkeep":{"enabled":true,"pending":0,"waiting":"waiting for quiet"}`) {
		t.Fatal(out)
	}
}

func TestStatusAndQueriesWithAMissingRepo(t *testing.T) {
	_, goLib := setupWorkspace(t)
	cli(t, "index", "--workspace", "fx")
	if err := os.Rename(goLib, goLib+".away"); err != nil {
		t.Fatal(err)
	}
	out := cli(t, "status", "--workspace", "fx")
	if !strings.Contains(out, "go-lib  missing: "+goLib+" is gone; index kept (main@") || !strings.Contains(out, "fresh:no") {
		t.Fatal(out)
	}
	out = cli(t, "find", "isHealthy", "--workspace", "fx", "--repo", "ts-domain")
	if !strings.Contains(out, "records.ts") {
		t.Fatal(out)
	}
	out = cli(t, "find", "MemStore", "--workspace", "fx")
	if !strings.Contains(out, "store/store.go") || !strings.Contains(out, "directory missing") {
		t.Fatal(out)
	}
}

func TestStatusTakesTheDaemonsFreshness(t *testing.T) {
	_, goLib := setupWorkspace(t)
	startDaemon(t, "--workspace", "fx")
	if err := os.WriteFile(filepath.Join(goLib, "store", "dirty.go"), []byte("package store\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second) // the daemon's debounce is 300 ms
	idle := func(r daemon.RepoStatus) bool { return r.Fresh && r.Queue == 0 }
	waitFor(t, "the edit indexed", func() bool { return daemonReports(t, "go-lib", idle) && daemonReports(t, "ts-domain", idle) })
	if out := cli(t, "status", "--workspace", "fx"); !strings.HasPrefix(out, "go-lib@0b29eeb ts-domain@43abdcd dirty\n") ||
		!strings.Contains(out, " dirty:1 ") {
		t.Fatalf("a dirty tree the daemon indexed is not fresh: %s", out)
	}
}

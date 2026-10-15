package service

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/muralx/codedoc/internal/daemon"
	"github.com/muralx/codedoc/internal/session"
	"github.com/muralx/codedoc/internal/upkeep"
)

// upkeepHarness is a running daemon over go-lib with upkeep enabled,
// looking back one commit on a first run, and the test binary as claude.
type upkeepHarness struct {
	*harness
	record string
}

func newUpkeepHarness(t *testing.T, section string) *upkeepHarness {
	t.Helper()
	h := &upkeepHarness{harness: newHarness(t, "    upkeep: "+section+"\n", "go-lib"),
		record: filepath.Join(t.TempDir(), "claude")}
	t.Setenv(stubEnv, "ok")
	t.Setenv(stubRecordEnv, h.record)
	t.Setenv(stubGateEnv, filepath.Join(t.TempDir(), "gate"))
	h.git("go-lib", "config", "user.name", "Repo Owner")
	h.git("go-lib", "config", "user.email", "owner@example.com")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	o := h.options()
	o.ClaudeBin = exe
	h.start(o)
	return h
}

// commit adds the undocumented exported function name to package store
// in its own file and commits it.
func (h *upkeepHarness) commit(name string) {
	h.t.Helper()
	h.write("go-lib", "store/"+strings.ToLower(name)+".go", "package store\n\nfunc "+name+"() int {\n\treturn 1\n}\n")
	h.git("go-lib", "add", "-A")
	h.git("go-lib", "commit", "-q", "-m", "add "+name)
}

// up returns the upkeep status of go-lib.
func (h *upkeepHarness) up() daemon.UpkeepStatus { return h.repoStatus("go-lib").Upkeep }

// runs counts the claude invocations so far.
func (h *upkeepHarness) runs() int {
	b, _ := os.ReadFile(h.record)
	return strings.Count(string(b), "\n")
}

// gitOut returns the output of git in go-lib.
func (h *upkeepHarness) gitOut(args ...string) string {
	h.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = h.repos["go-lib"]
	out, err := cmd.CombinedOutput()
	if err != nil {
		h.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// read returns a file of go-lib.
func (h *upkeepHarness) read(rel string) string {
	h.t.Helper()
	b, err := os.ReadFile(filepath.Join(h.repos["go-lib"], filepath.FromSlash(rel)))
	if err != nil {
		h.t.Fatal(err)
	}
	return string(b)
}

// settle waits until the daemon indexed what the test changed.
func (h *upkeepHarness) settle() {
	h.t.Helper()
	time.Sleep(150 * time.Millisecond) // the debounce is 50 ms
	h.eventually("the change indexed", waitLimit, func() bool { return h.repoStatus("go-lib").Queue == 0 })
}

// same fails the test, with the daemon's log, when got differs from
// want.
func same[T comparable](h *upkeepHarness, what string, got, want T) {
	h.t.Helper()
	if got != want {
		h.t.Fatalf("%s: got %v, want %v\nupkeep: %+v\nlog:\n%s", what, got, want, h.up(), h.log.String())
	}
}

func TestUpkeepDocumentsCommittedChangesOnceQuietWhateverTheSessionLock(t *testing.T) {
	h := newUpkeepHarness(t, "{enabled: true, first_run_commits: 1}")
	lock, err := session.Lock(filepath.Dir(h.cfg.DBPath("fx")), "fx", "go-lib")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	same(h, "status at start", h.up(), daemon.UpkeepStatus{Enabled: true, Waiting: upkeep.WaitQuiet})
	h.commit("Fresh")
	h.settle()
	h.clock.advance(4 * time.Minute)
	time.Sleep(150 * time.Millisecond)
	same(h, "runs before the quiet time passed", h.runs(), 0)
	same(h, "waiting", h.up().Waiting, upkeep.WaitQuiet)
	h.clock.advance(time.Minute)
	h.eventually("the upkeep run, a session lock held", waitLimit, func() bool { return !h.up().LastRun.IsZero() })
	same(h, "status after the run", h.up(), daemon.UpkeepStatus{Enabled: true, Waiting: upkeep.WaitQuiet, SpentToday: 0.25,
		LastRun: h.clock.Now().UTC()})
	same(h, "claude runs", h.runs(), 1)
	same(h, "the docs commit", h.gitOut("log", "-1", "--format=%an|%s|%b"),
		"Repo Owner|docs: document 1 changed symbol|go-lib:go:github.com/fixture/lib/store:Fresh")
	same(h, "git status", h.gitOut("status", "--porcelain"), "")
	same(h, "commits", h.gitOut("rev-list", "--count", "HEAD"), "3")
	same(h, "the run logged", strings.Contains(h.log.String(),
		"fx/go-lib: upkeep: documented 1, skipped 0, failed 0, cost $0.2500 on main (commit "), true)
}

func TestUpkeepStopsAtTheDailyCapUntilTheNextDay(t *testing.T) {
	h := newUpkeepHarness(t, "{enabled: true, first_run_commits: 1, daily_cost_usd: 0.25}")
	h.commit("Fresh")
	h.settle()
	h.clock.advance(5 * time.Minute)
	h.eventually("the first run", waitLimit, func() bool { return !h.up().LastRun.IsZero() })
	h.commit("Second")
	h.settle()
	h.clock.advance(5 * time.Minute)
	h.eventually("the daily cap in status", waitLimit, func() bool { return h.up().CapReached })
	time.Sleep(150 * time.Millisecond)
	same(h, "runs past the daily cap", h.runs(), 1)
	same(h, "waiting at the cap", h.up().Waiting, upkeep.WaitCap)
	h.clock.advance(24 * time.Hour)
	h.eventually("a run the next day", waitLimit, func() bool { return h.runs() == 2 && h.up().SpentToday == 0.25 })
	h.eventually("its commit", waitLimit, func() bool { return h.gitOut("rev-list", "--count", "HEAD") == "5" })
	same(h, "the second docs commit", h.gitOut("log", "-1", "--format=%s"), "docs: document 1 changed symbol")
}

func TestUpkeepRunIsUndoneWhenTheRepoChangesUnderIt(t *testing.T) {
	for name, tc := range map[string]struct {
		change func(h *upkeepHarness)
		undo   []string
		why    string
	}{
		"a file": {func(h *upkeepHarness) { h.write("go-lib", "NOTES.txt", "mine\n") }, []string{"clean", "-fdq"},
			"run undone: NOTES.txt changed during the run; it will be retried"},
		"HEAD": {func(h *upkeepHarness) { h.git("go-lib", "commit", "-q", "--allow-empty", "-m", "mine") }, []string{"status"},
			"run undone: HEAD moved during the run; it will be retried"},
	} {
		t.Run(name, func(t *testing.T) {
			h := newUpkeepHarness(t, "{enabled: true, first_run_commits: 1}")
			t.Setenv(stubEnv, "wait")
			h.commit("Fresh")
			before := h.read("store/fresh.go")
			h.settle()
			h.clock.advance(5 * time.Minute)
			h.eventually("claude at work", waitLimit, func() bool { return h.runs() == 1 && h.up().Running })
			h.settle() // the run's own write is indexed and does not stop it
			same(h, "status while the run's own write is indexed", h.up(), daemon.UpkeepStatus{Enabled: true, Running: true, Pending: 1})
			tc.change(h)
			h.eventually("the run undone", waitLimit, func() bool { return h.up().LastError != "" })
			same(h, "status after the undone run", h.up(), daemon.UpkeepStatus{Enabled: true, Pending: 1, SpentToday: 1,
				Waiting: upkeep.WaitQuiet, LastError: tc.why})
			same(h, "fresh.go", h.read("store/fresh.go"), before)
			same(h, "docs commits", h.gitOut("log", "--format=%s", "--grep=^docs:"), "")
			h.gitOut(tc.undo...)
			h.settle()
			t.Setenv(stubEnv, "ok")
			h.clock.advance(5 * time.Minute)
			h.eventually("the retry", waitLimit, func() bool { return !h.up().LastRun.IsZero() })
			same(h, "error after the retry", h.up().LastError, "")
			same(h, "the docs commit", h.gitOut("log", "-1", "--format=%s"), "docs: document 1 changed symbol")
		})
	}
}

func TestUpkeepRunsOncePerCommitWhenItsCommitFails(t *testing.T) {
	h := newUpkeepHarness(t, "{enabled: true, first_run_commits: 1}")
	h.commit("Fresh")
	before := h.read("store/fresh.go")
	hook := filepath.Join(h.repos["go-lib"], ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho refused >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.settle()
	h.clock.advance(5 * time.Minute)
	h.eventually("the failed run", waitLimit, func() bool { return h.up().LastError != "" })
	same(h, "the error names the retry rule", strings.HasSuffix(h.up().LastError, "refused; no other run before the next commit"), true)
	same(h, "fresh.go", h.read("store/fresh.go"), before)
	same(h, "git status", h.gitOut("status", "--porcelain"), "")
	for range 2 {
		h.clock.advance(5 * time.Minute)
		time.Sleep(150 * time.Millisecond)
	}
	same(h, "runs at the commit that failed", h.runs(), 1)
	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}
	h.commit("Second")
	h.settle()
	h.clock.advance(5 * time.Minute)
	h.eventually("a run at the next commit", waitLimit, func() bool { return !h.up().LastRun.IsZero() })
	same(h, "error after it", h.up().LastError, "")
	same(h, "the docs commit", h.gitOut("log", "-1", "--format=%s"), "docs: document 2 changed symbols")
}

func TestOwnPathKnowsJournaledFilesAndTheirTemporaries(t *testing.T) {
	own := map[string]bool{"/r/store/a.go": true}
	for p, want := range map[string]bool{"store/a.go": true, "store/.a.go.codedoc-123": true, "store/b.go": false,
		"store/.b.go.codedoc-1": false, ".a.go.codedoc-1": false, "store/.codedoc-1": false} {
		if got := ownPath(own, []string{"/elsewhere", "/r"}, p); got != want {
			t.Errorf("%s: %v", p, got)
		}
	}
	run := &upkeepRun{}
	if why := run.foreign("/r", daemon.Change{Rescan: true}); why != "the repo was rescanned during the run" {
		t.Error(why)
	}
	if why := run.foreign("/r", daemon.Change{Manifests: true}); why != "a manifest changed during the run" {
		t.Error(why)
	}
}

func TestUpkeepIsOffByDefault(t *testing.T) {
	h := newHarness(t, "", "go-lib")
	h.start(h.options())
	h.clock.advance(time.Hour)
	time.Sleep(100 * time.Millisecond)
	if up := h.repoStatus("go-lib").Upkeep; up != (daemon.UpkeepStatus{}) {
		t.Fatalf("%+v", up)
	}
	if strings.Contains(h.log.String(), "upkeep") {
		t.Fatalf("log:\n%s", h.log.String())
	}
}

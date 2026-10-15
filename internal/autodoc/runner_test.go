package autodoc

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestRunSuccessCommits(t *testing.T) {
	f := newFixture(t, "ok")
	opts := f.options()
	opts.Commit = true
	sum, err := Run(context.Background(), opts, fakeGaps{list: testGaps}, f.idx, Git{}, f)
	if err != nil {
		t.Fatal(err)
	}
	if sum != (Summary{Documented: 3, Cost: 0.5}) {
		t.Errorf("summary: %+v", sum)
	}
	if !strings.Contains(readFile(t, f.root, "a/a.go"), "// A2 does things.\nfunc A2()") {
		t.Errorf("a.go not documented:\n%s", readFile(t, f.root, "a/a.go"))
	}
	log := git(t, f.root, "log", "--format=%an|%s|%b", "-2")
	for _, want := range []string{"Repo Owner|docs(codedoc): document b|r:go:b:B1", "Repo Owner|docs(codedoc): document a|r:go:a:A1\nr:go:a:A2"} {
		if !strings.Contains(log, want) {
			t.Errorf("git log lacks %q:\n%s", want, log)
		}
	}
	if st := git(t, f.root, "status", "--porcelain"); st != "" {
		t.Errorf("tree dirty after commit: %s", st)
	}
	if runs, _ := os.ReadDir(filepath.Join(os.Getenv("XDG_STATE_HOME"), "codedoc", "runs")); len(runs) != 0 {
		t.Errorf("journals left behind: %v", runs)
	}
	checkInvocation(t, f)
}

// checkInvocation verifies the claude command line, directory and env.
func checkInvocation(t *testing.T, f *fixture) {
	t.Helper()
	data, err := os.ReadFile(f.record)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 invocations, got %d", len(lines))
	}
	var rec struct {
		Args          []string
		Dir, Headless string
		Run, Path     string
	}
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Args[0] != "-p" || !slices.Equal(rec.Args[2:], claudeArgs("", 2, DefaultMaxCostUSD, "")[2:]) ||
		!strings.Contains(rec.Args[1], "- r:go:a:A1 (missing, a/a.go:3)\n- r:go:a:A2 (missing, a/a.go:5)\n") {
		t.Errorf("args: %q", rec.Args)
	}
	if rec.Dir != f.root || rec.Headless != "1" || !strings.HasPrefix(rec.Run, "autodoc-") {
		t.Errorf("dir %s headless %q run %q", rec.Dir, rec.Headless, rec.Run)
	}
	exe, _ := os.Executable()
	if !strings.HasPrefix(rec.Path, filepath.Dir(exe)+string(os.PathListSeparator)) {
		t.Errorf("PATH does not start with codedoc's directory: %s", rec.Path)
	}
}

func TestRunBrokenDocRestored(t *testing.T) {
	f := newFixture(t, "broken")
	opts := f.options()
	opts.Commit = true
	head := git(t, f.root, "rev-parse", "HEAD")
	sum, err := Run(context.Background(), opts, fakeGaps{list: testGaps}, f.idx, Git{}, f)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Failed != 3 || sum.Documented != 0 {
		t.Errorf("summary: %+v", sum)
	}
	if readFile(t, f.root, "a/a.go") != srcA || readFile(t, f.root, "b/b.go") != srcB {
		t.Error("files not restored")
	}
	if git(t, f.root, "rev-parse", "HEAD") != head {
		t.Error("failed run committed")
	}
	if !strings.Contains(f.log.String(), `r:go:a:A1 is "stale"`) {
		t.Errorf("failure not logged:\n%s", f.log)
	}
	// check, then re-index after restore, per group
	if len(f.idx.reindex) != 4 || !slices.Equal(f.idx.reindex[1], []string{"a/a.go"}) {
		t.Errorf("reindex calls: %v", f.idx.reindex)
	}
}

func TestRunRefusesDirtyTree(t *testing.T) {
	f := newFixture(t, "ok")
	writeFile(t, f.root, "notes.txt", "mine\n")
	_, err := Run(context.Background(), f.options(), fakeGaps{list: testGaps}, f.idx, Git{}, f)
	if !errors.Is(err, ErrDirty) {
		t.Fatalf("got %v, want ErrDirty", err)
	}
	if _, err := os.Stat(f.record); !os.IsNotExist(err) {
		t.Error("claude ran on a dirty tree")
	}
}

func TestRunAllowDirtyKeepsUserEditOnFailure(t *testing.T) {
	f := newFixture(t, "broken")
	edited := srcA + "\n// user's own work in progress\nfunc A3() {}\n"
	writeFile(t, f.root, "a/a.go", edited)
	opts := f.options()
	opts.AllowDirty, opts.Commit = true, true
	head := git(t, f.root, "rev-parse", "HEAD")
	sum, err := Run(context.Background(), opts, fakeGaps{list: testGaps}, f.idx, Git{}, f)
	if err != nil || sum.Failed != 3 {
		t.Fatalf("summary %+v err %v", sum, err)
	}
	if got := readFile(t, f.root, "a/a.go"); got != edited {
		t.Errorf("user edit lost:\n%s", got)
	}
	if git(t, f.root, "rev-parse", "HEAD") != head {
		t.Error("allow-dirty run committed")
	}
}

func TestRunAllowDirtyDisablesCommit(t *testing.T) {
	f := newFixture(t, "ok")
	writeFile(t, f.root, "notes.txt", "mine\n")
	opts := f.options()
	opts.AllowDirty, opts.Commit = true, true
	head := git(t, f.root, "rev-parse", "HEAD")
	sum, err := Run(context.Background(), opts, fakeGaps{list: testGaps}, f.idx, Git{}, f)
	if err != nil || sum.Documented != 3 {
		t.Fatalf("summary %+v err %v", sum, err)
	}
	if git(t, f.root, "rev-parse", "HEAD") != head {
		t.Error("allow-dirty run committed")
	}
}

func TestRunMaxItems(t *testing.T) {
	f := newFixture(t, "ok")
	opts := f.options()
	opts.MaxItems = 2
	sum, err := Run(context.Background(), opts, fakeGaps{list: testGaps}, f.idx, Git{}, f)
	if err != nil || sum != (Summary{Documented: 2, Cost: 0.5}) {
		t.Fatalf("summary %+v err %v", sum, err)
	}
	if strings.Contains(readFile(t, f.root, "a/a.go"), "// A2") {
		t.Error("A2 documented beyond max items")
	}
}

func TestRunMaxMinutes(t *testing.T) {
	f := newFixture(t, "ok")
	clock := &fakeClock{now: time.Date(2026, 10, 9, 2, 0, 0, 0, time.UTC)}
	f.idx.clock, f.idx.step = clock, 31*time.Minute
	opts := f.options()
	opts.MaxMinutes, opts.Now = 30, clock.Now
	sum, err := Run(context.Background(), opts, fakeGaps{list: testGaps}, f.idx, Git{}, f)
	if err != nil || sum != (Summary{Documented: 2, Skipped: 1, Cost: 0.25}) {
		t.Fatalf("summary %+v err %v", sum, err)
	}
}

func TestRunGroupTimesOut(t *testing.T) {
	f := newFixture(t, "hang")
	opts := f.options()
	opts.MaxMinutes = 1
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	sum, err := Run(ctx, opts, fakeGaps{list: testGaps}, f.idx, Git{}, f)
	if err != nil || sum.Failed != 2 || sum.Skipped != 1 {
		t.Fatalf("summary %+v err %v", sum, err)
	}
}

func TestRunCancelledSkipsAll(t *testing.T) {
	f := newFixture(t, "ok")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sum, err := Run(ctx, f.options(), fakeGaps{list: testGaps}, f.idx, Git{}, f)
	if err != nil || sum != (Summary{Skipped: 3}) {
		t.Fatalf("summary %+v err %v", sum, err)
	}
}

func TestRunClaudeFailures(t *testing.T) {
	for _, mode := range []string{"error", "crash"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t, mode)
			opts := f.options()
			opts.Commit = true
			sum, err := Run(context.Background(), opts, fakeGaps{list: testGaps}, f.idx, Git{}, f)
			if err != nil || sum.Failed != 3 {
				t.Fatalf("summary %+v err %v", sum, err)
			}
			if readFile(t, f.root, "a/a.go") != srcA {
				t.Error("not restored")
			}
			if mode == "error" && sum.Cost != 0.5 {
				t.Errorf("cost of failed runs not counted: %v", sum.Cost)
			}
		})
	}
}

// failingCommit is a Committer whose commits fail.
type failingCommit struct{ Git }

func (failingCommit) Commit(string, []string, string) error { return errors.New("no identity") }

func TestRunCommitFailureRestoresAndStops(t *testing.T) {
	f := newFixture(t, "ok")
	opts := f.options()
	opts.Commit = true
	sum, err := Run(context.Background(), opts, fakeGaps{list: testGaps}, f.idx, failingCommit{}, f)
	if err == nil || sum.Failed != 2 || sum.Skipped != 1 {
		t.Fatalf("summary %+v err %v", sum, err)
	}
	if readFile(t, f.root, "a/a.go") != srcA {
		t.Error("not restored after failed commit")
	}
}

func TestRunReindexAndGapErrors(t *testing.T) {
	f := newFixture(t, "ok")
	f.idx.failNext = true
	sum, err := Run(context.Background(), f.options(), fakeGaps{list: testGaps}, f.idx, Git{}, f)
	if err != nil || sum.Failed != 2 || sum.Documented != 1 {
		t.Fatalf("summary %+v err %v", sum, err)
	}
	if _, err := Run(context.Background(), f.options(), fakeGaps{err: errors.New("db")}, f.idx, Git{}, nil); err == nil {
		t.Error("gaps error ignored")
	}
}

func TestGroupGaps(t *testing.T) {
	keys := func(gs []group) string {
		var parts []string
		for _, g := range gs {
			parts = append(parts, g.module+"="+strings.Join(g.keys(), ","))
		}
		return strings.Join(parts, " ")
	}
	for limit, want := range map[int]string{
		0: "a=r:go:a:A1,r:go:a:A2 b=r:go:b:B1",
		1: "a=r:go:a:A1",
		2: "a=r:go:a:A1,r:go:a:A2",
		3: "a=r:go:a:A1,r:go:a:A2 b=r:go:b:B1",
	} {
		if got := keys(groupGaps(testGaps, limit, 5)); got != want {
			t.Errorf("limit %d: %s", limit, got)
		}
	}
	if got := keys(groupGaps(testGaps, 0, 1)); got != "a=r:go:a:A1 a=r:go:a:A2 b=r:go:b:B1" {
		t.Errorf("size 1: %s", got)
	}
}

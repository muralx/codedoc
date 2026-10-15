package autodoc

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// runDirs lists the journals left in the fixture's state directory.
func runDirs(t *testing.T) []os.DirEntry {
	t.Helper()
	runs, _ := os.ReadDir(filepath.Join(os.Getenv("XDG_STATE_HOME"), "codedoc", "runs"))
	return runs
}

func TestRunRevertsStrayChanges(t *testing.T) {
	f := newFixture(t, "stray")
	opts := f.options()
	opts.Commit = true
	head := git(t, f.root, "rev-parse", "HEAD")
	sum, err := Run(context.Background(), opts, fakeGaps{list: testGaps}, f.idx, Git{}, f)
	if err != nil || sum.Failed != 3 || sum.Documented != 0 {
		t.Fatalf("summary %+v err %v", sum, err)
	}
	if st := git(t, f.root, "status", "--porcelain", "--untracked-files=all"); st != "" {
		t.Errorf("tree not reverted: %s", st)
	}
	if git(t, f.root, "rev-parse", "HEAD") != head {
		t.Error("failed run committed")
	}
	if !strings.Contains(f.log.String(), "wrote outside codedoc doc set: changed "+filepath.Join(f.root, "go.mod")) {
		t.Errorf("log:\n%s", f.log)
	}
	if runs := runDirs(t); len(runs) != 2 {
		t.Errorf("journals with the reverted copies not kept: %v", runs)
	}
}

func TestRunStopsWhenAPreDirtyFileChanges(t *testing.T) {
	f := newFixture(t, "dirty")
	writeFile(t, f.root, "notes.txt", "mine\n")
	opts := f.options()
	opts.AllowDirty = true
	sum, err := Run(context.Background(), opts, fakeGaps{list: testGaps}, f.idx, Git{}, f)
	if err == nil || !strings.Contains(err.Error(), "notes.txt") || sum.Failed != 2 || sum.Skipped != 1 {
		t.Fatalf("summary %+v err %v", sum, err)
	}
	if readFile(t, f.root, "notes.txt") != "overwritten\n" {
		t.Error("a file modified before the run was discarded")
	}
	if readFile(t, f.root, "a/a.go") != srcA {
		t.Error("journaled files not restored")
	}
}

func TestRunFailsOnWritesOutsideTheRoot(t *testing.T) {
	f := newFixture(t, "outside")
	other := t.TempDir()
	writeFile(t, other, "b/b.go", srcB)
	t.Setenv(stubOutsideEnv, other)
	sum, err := Run(context.Background(), f.options(), fakeGaps{list: testGaps}, f.idx, Git{}, f)
	if err != nil || sum.Failed != 3 {
		t.Fatalf("summary %+v err %v", sum, err)
	}
	if readFile(t, other, "b/b.go") != srcB || readFile(t, f.root, "a/a.go") != srcA {
		t.Error("files not restored")
	}
	if !strings.Contains(f.log.String(), "outside the repo root: "+filepath.Join(other, "b", "b.go")) {
		t.Errorf("log:\n%s", f.log)
	}
	if runs := runDirs(t); len(runs) != 2 {
		t.Errorf("journals of runs that wrote outside the root dropped: %v", runs)
	}
}

func TestRunFailsOnDocsOutsideTheGroup(t *testing.T) {
	f := newFixture(t, "drift")
	sum, err := Run(context.Background(), f.options(), fakeGaps{list: testGaps}, f.idx, Git{}, f)
	if err != nil || sum.Failed != 2 || sum.Documented != 1 {
		t.Fatalf("summary %+v err %v", sum, err)
	}
	if !strings.Contains(f.log.String(), "changed docs of keys outside the group: r:go:b:B1") {
		t.Errorf("log:\n%s", f.log)
	}
	if strings.Contains(readFile(t, f.root, "a/a.go"), "does things") {
		t.Error("failed group not restored")
	}
}

func TestRunKeepsFilesTheUserEditedDuringTheRun(t *testing.T) {
	f := newFixture(t, "broken-useredit")
	opts := f.options()
	opts.AllowDirty = true
	sum, err := Run(context.Background(), opts, fakeGaps{list: testGaps[:1]}, f.idx, Git{}, f)
	if err != nil || sum.Failed != 1 {
		t.Fatalf("summary %+v err %v", sum, err)
	}
	if !strings.HasSuffix(readFile(t, f.root, "a/a.go"), "// user\n") {
		t.Error("the user's edit was overwritten")
	}
	if !strings.Contains(f.log.String(), "kept "+filepath.Join(f.root, "a", "a.go")+" as edited") {
		t.Errorf("log:\n%s", f.log)
	}
}

func TestRunKillsTheProcessGroupOnTimeout(t *testing.T) {
	f := newFixture(t, "orphan")
	pidFile := filepath.Join(t.TempDir(), "child")
	t.Setenv(stubChildEnv, pidFile)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	sum, err := Run(ctx, f.options(), fakeGaps{list: testGaps[:1]}, f.idx, Git{}, f)
	if err != nil || sum.Failed != 1 || sum.UnknownCost != 1 {
		t.Fatalf("summary %+v err %v", sum, err)
	}
	if time.Since(start) > 4*time.Second {
		t.Errorf("run took %s: the orphaned child held its output open", time.Since(start))
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(string(data))
	deadline := time.Now().Add(3 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatal("the claude child survived the timeout")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestRunSplitsModulesIntoGroups(t *testing.T) {
	f := newFixture(t, "ok")
	opts := f.options()
	opts.GroupSize = 1
	sum, err := Run(context.Background(), opts, fakeGaps{list: testGaps}, f.idx, Git{}, f)
	if err != nil || sum.Documented != 3 || sum.Cost != 0.75 {
		t.Fatalf("summary %+v err %v", sum, err)
	}
	data, _ := os.ReadFile(f.record)
	if n := strings.Count(string(data), "\n"); n != 3 {
		t.Errorf("%d claude runs, want 3", n)
	}
	if !strings.Contains(string(data), `"--max-turns","18"`) {
		t.Errorf("turns not scaled to one key: %s", data)
	}
}

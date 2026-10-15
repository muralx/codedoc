package autodoc

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/muralx/codedoc/internal/docs/write"
)

// deadRun journals and documents key as run id would, then leaves its
// marker as a run that died before its clean end does.
func deadRun(t *testing.T, root, id, key string) {
	t.Helper()
	t.Setenv(write.RunIDEnv, id)
	m, err := markRun(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := document(root, key, "%s is BROKEN."); err != nil {
		t.Fatal(err)
	}
	if err := m.release(); err != nil {
		t.Fatal(err)
	}
	if err := os.Unsetenv(write.RunIDEnv); err != nil {
		t.Fatal(err)
	}
}

func TestRunRestoresLeftoverJournalsFirst(t *testing.T) {
	f := newFixture(t, "ok")
	deadRun(t, f.root, "autodoc-dead", "r:go:a:A1")
	t.Setenv(write.RunIDEnv, "autodoc-live")
	live, err := markRun("autodoc-live")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = live.release() }()
	if err := document(f.root, "r:go:b:B1", "%s is BROKEN."); err != nil {
		t.Fatal(err)
	}
	if err := os.Unsetenv(write.RunIDEnv); err != nil {
		t.Fatal(err)
	}
	opts := f.options()
	opts.AllowDirty = true
	if _, err := Run(context.Background(), opts, fakeGaps{}, f.idx, Git{}, f); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, f.root, "a/a.go"); got != srcA {
		t.Errorf("leftover not restored:\n%s", got)
	}
	if !strings.Contains(f.log.String(), "restored "+filepath.Join(f.root, "a", "a.go")+" from unfinished run autodoc-dead") {
		t.Errorf("restore not logged:\n%s", f.log)
	}
	if dir, _ := write.RunDir("autodoc-dead"); exists(dir) {
		t.Error("the restored journal was kept")
	}
	if readFile(t, f.root, "b/b.go") == srcB {
		t.Error("the journal of a live run was restored")
	}
}

func TestLeftoverKeepsFilesEditedSince(t *testing.T) {
	f := newFixture(t, "ok")
	deadRun(t, f.root, "autodoc-dead", "r:go:a:A1")
	writeFile(t, f.root, "a/a.go", srcA+"// mine\n")
	RecoverLeftovers(f)
	if got := readFile(t, f.root, "a/a.go"); got != srcA+"// mine\n" {
		t.Errorf("a later edit was overwritten:\n%s", got)
	}
	if !strings.Contains(f.log.String(), "kept "+filepath.Join(f.root, "a", "a.go")) {
		t.Errorf("kept file not logged:\n%s", f.log)
	}
	dir, _ := write.RunDir("autodoc-dead")
	if !exists(dir) || exists(filepath.Join(dir, markerName)) {
		t.Error("the journal of a kept file must stay, without its marker")
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestRunCancelledRestoresAClaudeIgnoringSignals(t *testing.T) {
	f := newFixture(t, "stubborn")
	pidFile := filepath.Join(t.TempDir(), "pid")
	t.Setenv(stubChildEnv, pidFile)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		waitFor(t, pidFile)
		cancel()
	}()
	sum, err := Run(ctx, f.options(), fakeGaps{list: testGaps}, f.idx, Git{}, f)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Failed != 2 || sum.Skipped != 1 {
		t.Errorf("summary: %+v", sum)
	}
	if readFile(t, f.root, "a/a.go") != srcA {
		t.Error("the cancelled group was not restored")
	}
	if pid := readPid(t, pidFile); alive(pid) {
		t.Errorf("claude %d survived the cancellation", pid)
	}
}

func TestClaudeDiesWithCodedoc(t *testing.T) {
	f := newFixture(t, "stubborn")
	pidFile := filepath.Join(t.TempDir(), "pid")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe)
	cmd.Dir = f.root
	cmd.Env = append(os.Environ(), stubChildEnv+"="+pidFile, invokeHelperEnv+"=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, pidFile)
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	pid := readPid(t, pidFile)
	deadline := time.Now().Add(10 * time.Second)
	for alive(pid) {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("claude %d outlived codedoc", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitFor polls until path exists.
func waitFor(t *testing.T, path string) {
	deadline := time.Now().Add(20 * time.Second)
	for !exists(path) {
		if time.Now().After(deadline) {
			t.Errorf("timed out waiting for %s", path)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
}

func readPid(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

// alive reports a process that exists and is not a zombie.
func alive(pid int) bool {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	_, rest, _ := strings.Cut(string(data), ") ")
	return !strings.HasPrefix(rest, "Z")
}

func TestLeftoverErrorsAreLogged(t *testing.T) {
	f := newFixture(t, "ok")
	if _, err := markRun("a/b"); err == nil {
		t.Error("marked an invalid run id")
	}
	deadRun(t, f.root, "autodoc-corrupt", "r:go:a:A1")
	dir, _ := write.RunDir("autodoc-corrupt")
	if err := os.WriteFile(filepath.Join(dir, "manifest.jsonl"), []byte("{broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	deadRun(t, f.root, "autodoc-unreadable", "r:go:b:B1")
	locked, _ := write.RunDir("autodoc-unreadable")
	if err := os.Chmod(filepath.Join(locked, markerName), 0); err != nil {
		t.Fatal(err)
	}
	RecoverLeftovers(f)
	for _, want := range []string{"restoring unfinished run autodoc-corrupt", "restoring unfinished run autodoc-unreadable"} {
		if !strings.Contains(f.log.String(), want) {
			t.Errorf("log lacks %q:\n%s", want, f.log)
		}
	}
	runs, _ := write.RunsDir()
	if err := os.RemoveAll(runs); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runs, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := markRun("autodoc-x"); err == nil {
		t.Error("marked a run without a runs directory")
	}
	RecoverLeftovers(f)
	if !strings.Contains(f.log.String(), "looking for unfinished runs") {
		t.Errorf("unreadable runs directory not logged:\n%s", f.log)
	}
}

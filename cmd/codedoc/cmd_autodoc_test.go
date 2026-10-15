package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/muralx/codedoc/internal/testutil"
)

// stubClaudeEnv makes the test binary act as `claude -p` (TestMain calls
// stubClaude).
const stubClaudeEnv = "CODEDOC_TEST_STUB_CLAUDE"

// stubClaude documents every "- key (state, file:line)" line (a key has
// a colon and no space) of the -p prompt with one codedoc
// doc set --batch, as the prompt asks, run in process from the working
// directory, and prints a
// result envelope. It appends its arguments to $CODEDOC_TEST_STUB_CLAUDE
// when that names a file.
func stubClaude(args []string) int {
	if rec := os.Getenv(stubClaudeEnv); rec != "1" {
		f, err := os.OpenFile(rec, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = fmt.Fprintln(f, strings.Join(args, " "))
			_ = f.Close()
		}
	}
	var batch strings.Builder
	for i, a := range args {
		if a != "-p" || i+1 >= len(args) {
			continue
		}
		for _, line := range strings.Split(args[i+1], "\n") {
			key, ok := strings.CutPrefix(line, "- ")
			key, _, _ = strings.Cut(key, " (")
			if ok && strings.Contains(key, ":") && !strings.Contains(key, " ") {
				batch.WriteString("=== " + key + "\nDocuments " + key + " for the tests.\n")
			}
		}
	}
	root := newRootCmd()
	root.SetArgs([]string{"doc", "set", "--batch"})
	root.SetIn(strings.NewReader(batch.String()))
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if os.Getenv(stubSignalEnv) != "" {
		signal.Ignore(syscall.SIGINT, syscall.SIGTERM)
		_ = syscall.Kill(os.Getppid(), syscall.SIGTERM)
		time.Sleep(time.Minute)
		return 1
	}
	out, _ := json.Marshal(map[string]any{"type": "result", "is_error": false, "total_cost_usd": 0.5, "result": "ok"})
	fmt.Println(string(out))
	return 0
}

// stubSignalEnv makes the stub, once it documented its keys, ignore
// SIGINT and SIGTERM, send SIGTERM to codedoc and hang.
const stubSignalEnv = "CODEDOC_TEST_STUB_SIGNAL"

// useStubClaude points CODEDOC_CLAUDE_BIN at the test binary acting as
// claude.
func useStubClaude(t *testing.T, record string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEDOC_CLAUDE_BIN", exe)
	t.Setenv(stubClaudeEnv, record)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
}

// javaWorkspace indexes a copy of the java-lib fixture as workspace fx.
func javaWorkspace(t *testing.T) string {
	t.Helper()
	root := testutil.CopyFixture(t, "java-lib")
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("workspaces:\n  fx:\n    repos: [{path: "+root+"}]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEDOC_CONFIG", cfgPath)
	t.Setenv("CODEDOC_DATA", filepath.Join(t.TempDir(), "data"))
	cli(t, "index", "--workspace", "fx")
	return root
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestAutodocDocumentsAndCommits(t *testing.T) {
	root := javaWorkspace(t)
	gitOut(t, root, "config", "user.name", "t")
	gitOut(t, root, "config", "user.email", "t@x")
	useStubClaude(t, "1")
	out := cli(t, "autodoc", "--workspace", "fx", "--repo", "java-lib", "--max-items", "3", "--commit")
	if !strings.Contains(out, "documented 3, skipped 0, failed 0, cost $") {
		t.Fatal(out)
	}
	if log := gitOut(t, root, "log", "--format=%s"); !strings.Contains(log, "docs(codedoc): document ") {
		t.Fatal(log)
	}
	if st := gitOut(t, root, "status", "--porcelain"); st != "" {
		t.Fatalf("left uncommitted: %s", st)
	}
}

func TestAutodocRefusesDirtyTreeAndNeedsRepo(t *testing.T) {
	root := javaWorkspace(t)
	useStubClaude(t, "1")
	if err := os.WriteFile(filepath.Join(root, "NOTES.txt"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := runCLI(t, "autodoc", "--workspace", "fx", "--repo", "java-lib"); err == nil ||
		!strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := runCLI(t, "autodoc", "--workspace", "fx"); err == nil || err.Error() != "autodoc needs --repo" {
		t.Fatal(err)
	}
	if _, err := runCLI(t, "autodoc", "--workspace", "fx", "--repo", "java-lib", "--max-items", "0"); err == nil ||
		err.Error() != "--max-items must be a positive number" {
		t.Fatal(err)
	}
	out := cli(t, "autodoc", "--workspace", "fx", "--repo", "java-lib", "--max-items", "1", "--allow-dirty", "--commit")
	if !strings.Contains(out, "documented 1, ") || gitOut(t, root, "log", "--oneline", "-1") == "" {
		t.Fatal(out)
	}
	if n := gitOut(t, root, "rev-list", "--count", "HEAD"); n != "1" {
		t.Fatalf("--allow-dirty committed: %s commits", n)
	}
}

func TestAutodocSIGTERMRestoresAndStops(t *testing.T) {
	root := javaWorkspace(t)
	useStubClaude(t, "1")
	t.Setenv(stubSignalEnv, "1")
	before := gitOut(t, root, "status", "--porcelain")
	out := cli(t, "autodoc", "--workspace", "fx", "--repo", "java-lib", "--max-items", "3")
	if !strings.Contains(out, "documented 0, ") || strings.Contains(out, "failed 0,") {
		t.Fatal(out)
	}
	if st := gitOut(t, root, "status", "--porcelain"); st != before {
		t.Fatalf("the interrupted run was not restored: %s", st)
	}
}

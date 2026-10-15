//go:build integration

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const selfModule = "github.com/muralx/codedoc"

// accept runs a built codedoc binary against one workspace holding a copy
// of this repository.
type accept struct {
	bin   string
	repo  string
	env   []string
	model bool
}

// acceptResponse is the JSON form of a query answer.
type acceptResponse struct {
	Sections []struct {
		Title   string        `json:"title"`
		Entries []acceptEntry `json:"entries"`
	} `json:"sections"`
}

type acceptEntry struct {
	Key   string         `json:"key"`
	Loc   string         `json:"loc"`
	Text  string         `json:"text"`
	Doc   string         `json:"doc"`
	Tags  []string       `json:"tags"`
	Extra map[string]any `json:"extra"`
}

// entries returns the entries of every section.
func (r acceptResponse) entries() []acceptEntry {
	var all []acceptEntry
	for _, s := range r.Sections {
		all = append(all, s.Entries...)
	}
	return all
}

// moduleRoot is the directory holding go.mod, two levels above this file.
func moduleRoot(t *testing.T) string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

// copyRepo copies the source files under root (see sourceFiles) into a
// new git repo with one commit at a fixed date.
func copyRepo(t *testing.T, root string) string {
	dst := filepath.Join(t.TempDir(), "codedoc")
	for _, rel := range sourceFiles(t, root) {
		copyFile(t, filepath.Join(root, rel), filepath.Join(dst, rel))
	}
	git(t, dst, "init", "-q")
	git(t, dst, "add", "-A")
	git(t, dst, "-c", "user.name=acceptance", "-c", "user.email=acceptance@example.invalid",
		"commit", "-q", "-m", "snapshot")
	return dst
}

// copyFile copies one regular file; tracked files missing from the working
// tree and symlinks are skipped.
func copyFile(t *testing.T, src, dst string) {
	info, err := os.Lstat(src)
	if os.IsNotExist(err) || (err == nil && !info.Mode().IsRegular()) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, body, info.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
}

func git(t *testing.T, dir string, args ...string) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE=2026-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// newAccept builds codedoc, copies the repository and writes a config with
// workspace "self" holding the copy as repo "codedoc".
func newAccept(t *testing.T) *accept {
	root := moduleRoot(t)
	a := &accept{bin: filepath.Join(t.TempDir(), "codedoc"), repo: copyRepo(t, root)}
	build := exec.Command("go", "build", "-tags", "sqlite_fts5", "-o", a.bin, "./cmd/codedoc")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "config.yaml")
	body := fmt.Sprintf("workspaces:\n  self:\n    repos:\n      - path: %s\n        name: codedoc\n", a.repo)
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	drop := map[string]bool{"CODEDOC_CONFIG": true, "CODEDOC_DATA": true, "XDG_RUNTIME_DIR": true,
		"XDG_STATE_HOME": true, "CODEDOC_EMBEDDER": true}
	for _, kv := range os.Environ() {
		if !drop[strings.SplitN(kv, "=", 2)[0]] {
			a.env = append(a.env, kv)
		}
	}
	a.env = append(a.env, "CODEDOC_CONFIG="+cfg, "CODEDOC_DATA="+filepath.Join(tmp, "data"),
		"XDG_RUNTIME_DIR="+filepath.Join(tmp, "run"), "XDG_STATE_HOME="+filepath.Join(tmp, "state"))
	a.model = os.Getenv("CODEDOC_MODEL_DIR") != ""
	if !a.model {
		a.env = append(a.env, "CODEDOC_EMBEDDER=off")
	}
	return a
}

// command prepares codedoc with args in the repo copy.
func (a *accept) command(args ...string) *exec.Cmd {
	cmd := exec.Command(a.bin, args...)
	cmd.Dir = a.repo
	cmd.Env = a.env
	return cmd
}

// run runs codedoc and returns its stdout; a failure is reported with
// t.Errorf on the given test and yields the output so far.
func (a *accept) run(t *testing.T, args ...string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd := a.command(args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Errorf("codedoc %s: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	return stdout.String()
}

// timed runs codedoc, fails the test on error and logs the duration.
func (a *accept) timed(t *testing.T, label string, args ...string) string {
	t.Helper()
	start := time.Now()
	out := a.run(t, args...)
	if t.Failed() {
		t.FailNow()
	}
	t.Logf("timing %s: %s\n%s", label, time.Since(start).Round(time.Millisecond), out)
	return out
}

// json runs a query with --format json and decodes the answer.
func (a *accept) json(t *testing.T, args ...string) acceptResponse {
	t.Helper()
	out := a.run(t, append(args, "--format", "json")...)
	var r acceptResponse
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Errorf("codedoc %s: not JSON: %v\n%s", strings.Join(args, " "), err, out)
	}
	return r
}

// anyEntry reports whether some entry satisfies ok, or fails t listing the
// entries it saw.
func anyEntry(t *testing.T, what string, es []acceptEntry, ok func(acceptEntry) bool) {
	t.Helper()
	for _, e := range es {
		if ok(e) {
			return
		}
	}
	var seen []string
	for _, e := range es {
		seen = append(seen, e.Key+"  "+e.Loc)
	}
	t.Errorf("%s: no matching entry among %d:\n%s", what, len(es), strings.Join(seen, "\n"))
}

func contains(t *testing.T, what, out string, subs ...string) {
	t.Helper()
	for _, s := range subs {
		if !strings.Contains(out, s) {
			t.Errorf("%s: missing %q in:\n%s", what, s, out)
		}
	}
}

func hasTag(e acceptEntry, tag string) bool {
	for _, x := range e.Tags {
		if x == tag {
			return true
		}
	}
	return false
}

// insertAfterLine inserts text as a new line after line n (1-based).
func insertAfterLine(t *testing.T, path string, n int, text string) {
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitAfter(string(body), "\n")
	if n < 1 || n > len(lines) {
		t.Fatalf("%s has no line %d", path, n)
	}
	out := strings.Join(lines[:n], "") + text + "\n" + strings.Join(lines[n:], "")
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
}

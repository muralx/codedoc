package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/session"
)

// cliOK runs the CLI, fails the test on an error, and checks that the
// output holds every one of want.
func cliOK(t *testing.T, args []string, want ...string) string {
	t.Helper()
	out, err := runCLI(t, args...)
	if err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out)
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Fatalf("%v: missing %q in\n%s", args, w, out)
		}
	}
	return out
}

func TestLsCommand(t *testing.T) {
	_, goLib := indexed(t)
	t.Chdir(filepath.Join(goLib, "store"))
	cliOK(t, []string{"ls", "--sort", "name"}, "== ls (4)\nstore/\n  doc.go  2026-01-01 00:00  2 lines\n  null.go  ")
	cliOK(t, []string{"ls", "--sort", "name", "--long"}, "== ls (4)\nstore/\n  doc.go  2026-01-01 00:00  70B  2 lines  code/go  "+
		"Package store persists the fixture's key-value data.\n")
	out := cliOK(t, []string{"ls", "--workspace", "fx", "--kind", "test", "--kind", "other", "--sort", "size", "--reverse", "-l"},
		"\nstore/store_test.go  2026-01-01 00:00  ", "  test/go  1 symbol\n")
	if strings.Contains(out, "store/store.go") {
		t.Fatalf("--kind test lists code:\n%s", out)
	}
	cliOK(t, []string{"ls", "..", "--dirs", "--depth", "1", "--format", "json"}, `"loc":"store/"`, `"files":4`)
	cliOK(t, []string{"outline", ".", "--sort", "lines", "--reverse"},
		"store/doc.go  0 symbols, 2 lines  2026-01-01 00:00\nstore/null.go")
}

func TestLsCommandErrors(t *testing.T) {
	_, goLib := indexed(t)
	t.Chdir(goLib)
	out, err := runCLI(t, "ls", "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("%v\n%s", err, out)
	}
	// Nothing to offer is one line: what was not found and what lists
	// the files there are, with no header and nothing on stdout.
	stdout, stderr, code := exitOf(t, "ls", "nope")
	if code != exitFailed || stdout != "" ||
		stderr != "codedoc: ls nope: not found: no indexed file under it; codedoc ls --repo go-lib lists the indexed files\n" {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if _, err = runCLI(t, "ls", "--sort", "age"); err == nil {
		t.Fatal("--sort age accepted")
	}
}

func TestLinesAndLineAddressCommands(t *testing.T) {
	_, goLib := indexed(t)
	t.Chdir(goLib)
	cliOK(t, []string{"lines", "store/store.go", "26-27"},
		"store/store.go:26-27  in MemStore.Get (26-32)\n26\tfunc (m *MemStore) Get")
	cliOK(t, []string{"lines", "store/store.go", "26-27", "12", "28+1"},
		"store/store.go\n  :12  in Store.Get (12)\n12\t", "\n  :26-29  in MemStore.Get (26-32)\n26\tfunc (m *MemStore) Get")
	if _, err := runCLI(t, "lines", "store/store.go"); err == nil {
		t.Fatal("lines without a range accepted")
	}
	cliOK(t, []string{"symbol", "store/store.go:36", "--body"}, "MemStore.Put  store/store.go:35-38", "36\t\tm.data[key] = value")
}

// feedbackState points the feedback file at a temp state directory and
// returns its path.
func feedbackState(t *testing.T) string {
	t.Helper()
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	return filepath.Join(state, "codedoc", "feedback.jsonl")
}

func TestFeedbackCommandRecordsTheIndexState(t *testing.T) {
	_, goLib := indexed(t)
	file := feedbackState(t)
	t.Chdir(goLib)
	key := "go-lib:go:github.com/fixture/lib/store:MemStore.Get"
	out := cliOK(t, []string{"feedback", "body ends\none line early", "--key", key, "--cmd", "codedoc symbol MemStore.Get --body", "--verbose"})
	if out != "feedback recorded in "+file+"\n" {
		t.Fatalf("got %q", out)
	}
	raw, _ := os.ReadFile(file)
	for _, want := range []string{`"workspace":"fx"`, `"repo":"go-lib"`, `"head":"main@0b29eeb"`, `"dirty":false`, `"extractor":"10"`,
		`"text":"body ends\none line early"`, `"key":"` + key + `"`, `"indexed_hash":"`, `"current_hash":"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("missing %s in %s", want, raw)
		}
	}
	t.Chdir(t.TempDir())
	cliOK(t, []string{"feedback", "outside any workspace", "--path", "x.go"})
	if _, err := runCLI(t, "feedback", "x", "--workspace", "nope"); err == nil {
		t.Fatal("an unknown --workspace is an error")
	}
	out = cliOK(t, []string{"feedback", "--list"},
		"Z  fx/go-lib main@0b29eeb  body ends one line early  [key:"+key+"] [cmd:codedoc symbol MemStore.Get --body] [codedoc ",
		"Z  outside any workspace  [path:x.go] [codedoc ")
	if !strings.HasPrefix(out, "== feedback (2)\n") {
		t.Fatalf("got:\n%s", out)
	}
}

func TestFeedbackCommandListsAndClears(t *testing.T) {
	indexed(t)
	file := feedbackState(t)
	cliOK(t, []string{"feedback", "first", "--workspace", "fx"})
	cliOK(t, []string{"feedback", "second", "--workspace", "fx", "--repo", "go-lib"})
	out := cliOK(t, []string{"feedback", "--list", "--since", "1h", "--format", "json"}, `{"sections":[{"title":"feedback",`,
		`"repo":"go-lib"`, `"text":"second","time":"`)
	if strings.Count(out, `"extra":{`) != 2 {
		t.Fatalf("got:\n%s", out)
	}
	if out := cliOK(t, []string{"feedback", "--list", "--since", "2999-01-01"}); out != "== feedback (0)\n" {
		t.Fatalf("got:\n%s", out)
	}
	for _, args := range [][]string{{"feedback"}, {"feedback", " "}, {"feedback", "--list", "--since", "soon"},
		{"feedback", "x", "--format", "xml"}, {"feedback", "--list", "--clear"}} {
		if out, err := runCLI(t, args...); err == nil {
			t.Errorf("%v accepted:\n%s", args, out)
		}
	}
	if out := cliOK(t, []string{"feedback", "--clear", "--verbose"}); out != "feedback cleared (2 reports)\n" {
		t.Fatalf("got %q", out)
	}
	if raw, _ := os.ReadFile(file); len(raw) != 0 {
		t.Fatalf("not cleared: %s", raw)
	}
}

func TestMCPFeedbackTool(t *testing.T) {
	e2eWorkspace(t)
	t.Setenv(session.HeadlessEnv, "1")
	feedbackState(t)
	cs, _ := mcpSession(t)
	got, isErr := mcpCall(t, cs, "codedoc_feedback", map[string]any{"text": "refs misses a caller", "workspace": "fx", "repo": "go-lib",
		"path": "go-lib:store/store.go"})
	if isErr || got != "ok" {
		t.Fatalf("%v\n%s", isErr, got)
	}
	got, isErr = mcpCall(t, cs, "codedoc_feedback", map[string]any{"list": true})
	if isErr || !strings.Contains(got, "fx/go-lib main@0b29eeb  refs misses a caller  [path:go-lib:store/store.go]") {
		t.Fatalf("%v\n%s", isErr, got)
	}
	if got, isErr = mcpCall(t, cs, "codedoc_feedback", map[string]any{}); !isErr || !strings.Contains(got, "text is required") {
		t.Fatalf("%v\n%s", isErr, got)
	}
}

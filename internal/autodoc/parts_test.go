package autodoc

import (
	"strings"
	"testing"
)

func TestPrompt(t *testing.T) {
	p := Prompt("auth/services", []Gap{{Key: "r:go:x:A", State: "missing", Path: "x/a.go", Line: 3},
		{Key: "r:x/", State: "stale", Path: "x"}})
	for _, want := range []string{"module auth/services", "- r:go:x:A (missing, x/a.go:3)\n- r:x/ (stale, x)\n",
		"cannot be resolved", "skip it", "Document exactly the keys listed above, each once. When",
		"only through these codedoc commands", "codedoc symbol <key> --body", "codedoc explain <key>",
		"codedoc callers <key> --no-tests", "codedoc tests <key>", "codedoc doc set --batch <<'DOCS'\n     === <key>\n",
		"in ONE call", "doc set prints nothing when the docs are written", "\"<key>: <what>; <what to do>\"",
		"What a good doc says", "stop"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q:\n%s", want, p)
		}
	}
	// The prompt is complete on its own: the codedoc-document skill is
	// for a live session, which writes the comment in its edit, and a
	// headless run cannot edit.
	if strings.Contains(p, "skill") {
		t.Errorf("the prompt refers to a skill:\n%s", p)
	}
}

func TestClaudeArgsConfineTheAgent(t *testing.T) {
	args := claudeArgs("p", 2, 1, "")
	if args[0] != "-p" || args[1] != "p" {
		t.Fatal(args)
	}
	got := strings.Join(args[2:], " ")
	want := "--output-format json --tools Bash,Read --allowedTools Bash(codedoc:*),Read " +
		"--disallowedTools Edit,Write,MultiEdit,NotebookEdit --permission-mode dontAsk --max-turns 26 --max-budget-usd 1.00"
	if got != want {
		t.Errorf("args:\n%s\nwant\n%s", got, want)
	}
	if got := strings.Join(claudeArgs("p", 9, -1, ""), " "); !strings.HasSuffix(got, "--max-turns 60") {
		t.Errorf("uncapped cost or turns: %s", got)
	}
	if got := strings.Join(claudeArgs("p", 2, 1, "sonnet"), " "); !strings.HasSuffix(got, "--max-budget-usd 1.00 --model sonnet") {
		t.Errorf("model: %s", got)
	}
}

func TestParseEnvelope(t *testing.T) {
	env, err := parseEnvelope([]byte("noise\n{\"is_error\":true,\"subtype\":\"error_max_turns\",\"total_cost_usd\":1.5,\"result\":\"r\",\"errors\":[\"e1\"]}\n"))
	if err != nil || !env.IsError || env.Cost != 1.5 || !env.reported {
		t.Errorf("%+v %v", env, err)
	}
	if got := env.failure(); got != "claude reported an error (subtype error_max_turns): r; e1" {
		t.Errorf("failure: %s", got)
	}
	if got := (Envelope{}).failure(); got != "claude reported an error (subtype unknown): " {
		t.Errorf("empty failure: %q", got)
	}
	if _, err := parseEnvelope([]byte("not json")); err == nil {
		t.Error("garbage accepted")
	}
}

func TestClaudeBin(t *testing.T) {
	t.Setenv(ClaudeBinEnv, "")
	if got := claudeBin(""); got != "claude" {
		t.Errorf("default: %s", got)
	}
	t.Setenv(ClaudeBinEnv, "/opt/stub")
	if got := claudeBin(""); got != "/opt/stub" {
		t.Errorf("env: %s", got)
	}
	if got := claudeBin("/x"); got != "/x" {
		t.Errorf("option: %s", got)
	}
}

func TestSummaryString(t *testing.T) {
	if got := (Summary{Documented: 2, Skipped: 1, Failed: 3, Cost: 0.125}).String(); got != "documented 2, skipped 1, failed 3, cost $0.1250" {
		t.Error(got)
	}
	if got := (Summary{Failed: 1, UnknownCost: 1}).String(); !strings.Contains(got, "cost $0.0000 plus unknown (1 claude run(s) timed out") {
		t.Error(got)
	}
}

func TestDocDrift(t *testing.T) {
	before := map[string]DocAt{"k1": {"a.go", "h1"}, "k2": {"a.go", ""}, "k3": {"b.go", "x"}, "gone": {"a.go", ""}}
	after := map[string]DocAt{"k1": {"a.go", "h2"}, "k2": {"a.go", "new"}, "k3": {"b.go", "y"}, "added": {"a.go", ""}}
	got := docDrift(before, after, []string{"a.go"}, []string{"k1"})
	if strings.Join(got, ",") != "added,gone,k2" {
		t.Errorf("drift %v", got)
	}
}

func TestGitCommitsOnlyGivenPaths(t *testing.T) {
	f := newFixture(t, "ok")
	writeFile(t, f.root, "a/a.go", srcA+"// x\n")
	writeFile(t, f.root, "b/b.go", srcB+"// y\n")
	git(t, f.root, "add", "b/b.go") // the user's own staged change
	writeFile(t, f.root, "new.txt", "n\n")
	if dirty, err := (Git{}).Dirty(f.root); err != nil || !dirty {
		t.Fatalf("dirty %v %v", dirty, err)
	}
	if err := (Git{}).Commit(f.root, []string{"a/a.go", "new.txt"}, "docs(codedoc): document a\n\nk\n"); err != nil {
		t.Fatal(err)
	}
	if got := git(t, f.root, "show", "--name-only", "--format=%s", "HEAD"); got != "docs(codedoc): document a\n\na/a.go\nnew.txt" {
		t.Errorf("commit: %q", got)
	}
	if st := git(t, f.root, "status", "--porcelain"); st != "M  b/b.go" {
		t.Errorf("status after commit: %q", st)
	}
	if err := (Git{}).Commit(f.root, nil, "x"); err != nil {
		t.Errorf("empty commit: %v", err)
	}
	if err := (Git{}).Commit(f.root, []string{"missing.go"}, "x"); err == nil {
		t.Error("committing a missing path succeeded")
	}
}

func TestRelTo(t *testing.T) {
	if _, ok := relTo([]string{"/r"}, "/other/x.go"); ok {
		t.Error("outside path accepted")
	}
	if got, ok := relTo([]string{"/r", "/real"}, "/real/a/b.go"); !ok || got != "a/b.go" {
		t.Errorf("%s %v", got, ok)
	}
}

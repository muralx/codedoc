package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/session"
)

// outsidePaths plants a file outside the workspace and returns the
// path arguments that lead to it from the repo go-lib: by climbing out
// of the repo, and through a symbolic link inside it.
func outsidePaths(t *testing.T, goLib string) []string {
	t.Helper()
	secret := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(secret, []byte("top secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(goLib, secret)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(goLib, "store", "link.txt")); err != nil {
		t.Fatal(err)
	}
	return []string{"go-lib:" + filepath.ToSlash(rel), "go-lib:store/link.txt", secret}
}

// refusal is what every refused path argument is answered with.
const refusal = "path is outside the repos of the workspace"

func TestPathArgumentsAreContainedOnTheCLIAndOverMCP(t *testing.T) {
	repos := e2eWorkspace(t)
	t.Setenv(session.HeadlessEnv, "1")
	file := feedbackState(t)
	cs, _ := mcpSession(t)
	for _, p := range outsidePaths(t, repos["go-lib"]) {
		calls := []struct {
			cli  []string
			tool string
			in   map[string]any
		}{
			{[]string{"lines", p, "1"}, "codedoc_lines", map[string]any{"path": p, "range": "1"}},
			{[]string{"outline", p}, "codedoc_outline", map[string]any{"target": p}},
			{[]string{"symbol", p + ":1", "--body"}, "codedoc_symbol", map[string]any{"symbol": p + ":1"}},
			{[]string{"ls", p}, "codedoc_ls", map[string]any{"paths": []string{p}}},
		}
		for _, c := range calls {
			out, err := runCLI(t, append(c.cli, "--workspace", "fx")...)
			if err == nil || strings.Contains(out, "top secret") || !strings.Contains(out+err.Error(), refusal) {
				t.Errorf("%v: err %v\n%s", c.cli, err, out)
			}
			c.in["workspace"] = "fx"
			got, _ := mcpCall(t, cs, c.tool, c.in)
			if strings.Contains(got, "top secret") || !strings.Contains(got, refusal) {
				t.Errorf("%s %v:\n%s", c.tool, c.in, got)
			}
		}
		cliOK(t, []string{"feedback", "cli report", "--path", p, "--workspace", "fx"})
		if got, isErr := mcpCall(t, cs, "codedoc_feedback", map[string]any{"text": "mcp report", "path": p, "workspace": "fx"}); isErr {
			t.Errorf("codedoc_feedback %s:\n%s", p, got)
		}
	}
	raw, err := os.ReadFile(file)
	if err != nil || strings.Count(string(raw), "\n") != 6 || strings.Contains(string(raw), "_hash") {
		t.Fatalf("%v: a report about a file outside the workspace carries no hash:\n%s", err, raw)
	}
}

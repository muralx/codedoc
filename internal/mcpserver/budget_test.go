package mcpserver

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestBudgetDefaultsAndCap(t *testing.T) {
	headless(t)
	rec := &recorder{}
	cs, _ := connect(t, Options{Exec: rec.exec, Log: &lines{}})
	for _, c := range []struct {
		tool string
		in   map[string]any
		want string
	}{
		{"codedoc_find", map[string]any{"pattern": "G"}, "--budget=6000"},
		{"codedoc_find", map[string]any{"pattern": "G", "budget": 0}, "--budget=60000"},
		{"codedoc_find", map[string]any{"pattern": "G", "budget": 1000000}, "--budget=60000"},
		{"codedoc_find", map[string]any{"pattern": "G", "budget": 100}, "--budget=100"},
		// impact is a summary: its default is the one of the CLI command.
		{"codedoc_impact", map[string]any{"symbol": "G"}, "--budget=3000"},
		{"codedoc_impact", map[string]any{"symbol": "G", "budget": 0}, "--budget=60000"},
	} {
		rec.args = nil
		callText(t, cs, c.tool, c.in)
		if !slices.Contains(rec.args[0], c.want) {
			t.Errorf("%v: %v lacks %s", c.in, rec.args[0], c.want)
		}
	}
}

func TestPanicIsAToolError(t *testing.T) {
	headless(t)
	log := &lines{}
	calls := 0
	cs, _ := connect(t, Options{Log: log, Exec: func(context.Context, []string, io.Reader, io.Writer, io.Writer) error {
		calls++
		if calls == 1 {
			panic("boom")
		}
		return nil
	}})
	if text, isErr := callText(t, cs, "codedoc_overview", nil); !isErr || !strings.Contains(text, "internal error in codedoc_overview: boom") {
		t.Fatalf("%v %q", isErr, text)
	}
	if !strings.Contains(log.String(), "codedoc_overview: panic: boom") {
		t.Fatalf("log: %s", log)
	}
	if _, isErr := callText(t, cs, "codedoc_overview", nil); isErr {
		t.Fatal("server unusable after a panic")
	}
}

func TestOutsideWorkspaceSuggestsTheWorkspaceInput(t *testing.T) {
	headless(t)
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfg, []byte("workspaces:\n  fx:\n    repos: [{path: "+t.TempDir()+"}]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEDOC_CONFIG", cfg)
	rec := &recorder{err: errors.New("/tmp/x is not inside any configured repository")}
	cs, _ := connect(t, Options{Exec: rec.exec, Log: &lines{}})
	text, isErr := callText(t, cs, "codedoc_overview", nil)
	if !isErr || !strings.HasSuffix(text, "codedoc: pass the workspace input to choose a workspace (configured: fx)\n") {
		t.Fatalf("%v %q", isErr, text)
	}
	if workspaceHint(errors.New("other")) != "" {
		t.Fatal("hint for an unrelated error")
	}
}

func TestDocKindEnum(t *testing.T) {
	headless(t)
	rec := &recorder{}
	cs, _ := connect(t, Options{Exec: rec.exec, Log: &lines{}})
	if _, isErr := callText(t, cs, "codedoc_doc", map[string]any{"action": "gaps", "kind": "bogus"}); !isErr {
		t.Fatal("unknown doc kind accepted")
	}
	if _, isErr := callText(t, cs, "codedoc_find", map[string]any{"pattern": "G", "kind": "method"}); isErr {
		t.Fatal("find kind restricted by the doc enum")
	}
}

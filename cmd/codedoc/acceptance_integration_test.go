//go:build integration

package main

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The acceptance test indexes a copy of this repository with a freshly
// built codedoc binary and checks answers that must hold for the real code.
// It needs scip-go on PATH (the Docker integration image has it); on a host
// without it, set CODEDOC_ACCEPTANCE_HOST=1 to skip the SCIP step:
//
//	CODEDOC_ACCEPTANCE_HOST=1 go test -tags "sqlite_fts5 integration" -run TestAcceptance -v ./cmd/codedoc/
//
// With CODEDOC_MODEL_DIR set the real embedding model is used; without it
// embeddings are off and search is text-only.

// TestAcceptance indexes this repository and checks answers that hold for
// the real code. The steps share one index and run in order: read-only
// queries first, then SCIP and MCP, then the steps that edit the copy.
func TestAcceptance(t *testing.T) {
	a := newAccept(t)
	t.Logf("embedding model: %v", a.model)
	a.timed(t, "index", "index")
	a.timed(t, "no-op index", "index")
	steps := []struct {
		name string
		fn   func(*testing.T)
	}{
		{"status", a.status}, {"find", a.find}, {"callers", a.callers}, {"outline", a.outline},
		{"config", a.config}, {"overview", a.overview}, {"search", a.search}, {"gaps", a.gaps},
		{"explain", a.explain}, {"flow", a.flow}, {"symbol-body", a.symbolBody},
		{"scip", a.scip}, {"mcp", a.mcp}, {"changes", a.changes}, {"doc-set", a.docSet},
	}
	for _, s := range steps {
		t.Run(s.name, s.fn)
	}
}

func (a *accept) status(t *testing.T) {
	out := a.run(t, "status")
	if !regexp.MustCompile(`(?m)^codedoc\s.*files:[1-9]\d*.*symbols:[1-9]\d*.*indexed:\S+`).MatchString(out) {
		t.Errorf("status does not show repo codedoc indexed:\n%s", out)
	}
}

func (a *accept) find(t *testing.T) {
	es := a.json(t, "find", "Render", "--kind", "function").entries()
	anyEntry(t, "find Render", es, func(e acceptEntry) bool {
		return e.Key == "codedoc:go:"+selfModule+"/internal/output:Render"
	})
}

func (a *accept) callers(t *testing.T) {
	es := a.json(t, "callers", "index.Repo", "--budget", "0").entries()
	anyEntry(t, "callers index.Repo from cmd/codedoc", es, func(e acceptEntry) bool {
		return strings.HasPrefix(e.Loc, "cmd/codedoc/") && !strings.Contains(e.Loc, "_test.go")
	})
}

func (a *accept) outline(t *testing.T) {
	es := a.json(t, "outline", "internal/store/store.go").entries()
	anyEntry(t, "outline store.go", es, func(e acceptEntry) bool { return strings.HasSuffix(e.Key, "/internal/store:Open") })
}

func (a *accept) config(t *testing.T) {
	es := a.json(t, "config", "CODEDOC_DATA", "--budget", "0").entries()
	anyEntry(t, "config CODEDOC_DATA reads in internal/config", es, func(e acceptEntry) bool {
		return strings.HasPrefix(e.Loc, "internal/config/")
	})
	for key, loc := range map[string]string{
		"CODEDOC_CLAUDE_BIN": "internal/autodoc/claude.go:", "CODEDOC_HEADLESS": "internal/session/session.go:",
	} {
		es := a.json(t, "config", key, "--budget", "0").entries()
		anyEntry(t, "config "+key+" read through a constant in "+loc, es, func(e acceptEntry) bool {
			return strings.HasPrefix(e.Loc, loc)
		})
	}
}

func (a *accept) overview(t *testing.T) {
	r := a.json(t, "overview", "--budget", "0")
	for _, s := range r.Sections {
		if strings.Contains(s.Title, "entry points") {
			anyEntry(t, "overview entry points", s.Entries, func(e acceptEntry) bool {
				return strings.Contains(e.Key, "cmd/codedoc") || strings.HasPrefix(e.Loc, "cmd/codedoc")
			})
			return
		}
	}
	t.Errorf("overview has no entry points section: %+v", r)
}

func (a *accept) search(t *testing.T) {
	limit := "10"
	if a.model {
		limit = "5"
	}
	es := a.json(t, "search", "byte budget", "--limit", limit).entries()
	for i, e := range es {
		t.Logf("search #%d %s", i+1, e.Key)
	}
	anyEntry(t, "search \"byte budget\" top "+limit, es, func(e acceptEntry) bool {
		return strings.Contains(e.Key, "internal/output") || strings.HasPrefix(e.Loc, "internal/output")
	})
}

func (a *accept) gaps(t *testing.T) {
	if es := a.json(t, "gaps", "--limit", "5").entries(); len(es) != 5 {
		t.Errorf("gaps --limit 5: %d entries: %+v", len(es), es)
	}
}

func (a *accept) explain(t *testing.T) {
	// The stable key is printed on request; the default names the symbol.
	out := a.run(t, "explain", "output.Render", "--keys")
	contains(t, "explain output.Render --keys", out, "internal/output:Render", selfModule+"/internal/output", "top callers")
	contains(t, "explain output.Render", a.run(t, "explain", "output.Render"), "== explain Render (", "\n      Render  internal/output/frame.go:")
}

func (a *accept) flow(t *testing.T) {
	out := a.run(t, "flow", "codedoc:cmd/codedoc/main.go#main", "--depth", "2", "--keys")
	contains(t, "flow main --keys", out, "cmd/codedoc:main", "cmd/codedoc:newRootCmd")
	out = a.run(t, "flow", "codedoc:cmd/codedoc/main.go#main", "--depth", "2")
	contains(t, "flow main", out, "== flow from main (", "\nmain  cmd/codedoc/main.go:", "\n  newRootCmd  cmd/codedoc/main.go:")
}

func (a *accept) symbolBody(t *testing.T) {
	out := a.run(t, "symbol", "output.Render", "--body")
	contains(t, "symbol output.Render --body", out, "func Render(")
	if !regexp.MustCompile(`(?m)^\d+\t\s*return `).MatchString(out) {
		t.Errorf("symbol --body has no numbered body lines:\n%s", out)
	}
}

// scip runs the real SCIP indexers and expects precise callers of Render.
func (a *accept) scip(t *testing.T) {
	if _, err := exec.LookPath("scip-go"); err != nil {
		if os.Getenv("CODEDOC_ACCEPTANCE_HOST") == "1" {
			t.Skip("scip-go not on PATH (CODEDOC_ACCEPTANCE_HOST=1)")
		}
		t.Fatalf("scip-go not on PATH: %v", err)
	}
	out := a.timed(t, "scip run", "scip", "run", "--repo", "codedoc")
	if !regexp.MustCompile(`go: ran precise:\+[1-9]`).MatchString(out) {
		t.Errorf("scip run merged no Go edges:\n%s", out)
	}
	// The confidence tier of a certain edge is a tag of --detail (the
	// default prints only the tags that change what the reader does).
	callers := a.run(t, "callers", "output.Render", "--budget", "0", "--detail")
	contains(t, "callers output.Render --detail after scip run", callers, "[precise]")
	if plain := a.run(t, "callers", "output.Render", "--budget", "0"); strings.Contains(plain, "[precise]") ||
		!strings.Contains(plain, "== callers of Render (") {
		t.Errorf("callers output.Render without --detail:\n%s", plain)
	}
}

// mcp talks JSON-RPC to codedoc mcp over stdio.
func (a *accept) mcp(t *testing.T) {
	cmd := a.command("mcp")
	cmd.Stderr = os.Stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	r := &rpc{t: t, in: in, out: bufio.NewScanner(stdout)}
	r.out.Buffer(nil, 1<<22)
	r.send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18",` +
		`"capabilities":{},"clientInfo":{"name":"acceptance","version":"0"}}}`)
	contains(t, "initialize", stringify(r.reply()), `"name":"codedoc"`)
	r.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	r.send(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if list := stringify(r.reply()); strings.Count(list, `"name":"codedoc_`) != 19 {
		t.Errorf("tools/list does not list 19 tools: %s", list)
	}
	r.send(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"codedoc_find",` +
		`"arguments":{"pattern":"Render","kind":"function"}}}`)
	call := stringify(r.reply())
	contains(t, "tools/call codedoc_find", call, "== find Render (", "Render  internal/output/frame.go:")
	if strings.Contains(call, `"isError":true`) {
		t.Errorf("tools/call codedoc_find is an error: %s", call)
	}
	_ = in.Close()
	if err := cmd.Wait(); err != nil {
		t.Errorf("codedoc mcp exited with %v", err)
	}
}

// changes edits the body of output.Render and expects changes to tag it.
func (a *accept) changes(t *testing.T) {
	es := a.json(t, "symbol", "output.Render").entries()
	if len(es) != 1 {
		t.Fatalf("symbol output.Render: %+v", es)
	}
	path := filepath.Join(a.repo, "internal/output/frame.go")
	insertAfterLine(t, path, int(es[0].Extra["start_line"].(float64)), "\tbudget += 0")
	es = a.json(t, "changes", "--since", "HEAD", "--budget", "0").entries()
	anyEntry(t, "changes --since HEAD tags Render body-changed", es, func(e acceptEntry) bool {
		return strings.HasSuffix(e.Key, "/internal/output:Render") && hasTag(e, "body-changed")
	})
}

// docSet documents an undocumented function of the copy and reads it back.
func (a *accept) docSet(t *testing.T) {
	var key string
	for _, e := range a.json(t, "gaps", "--limit", "200").entries() {
		kind, state := e.Extra["kind"], e.Extra["state"]
		if (kind == "function" || kind == "method") && state == "missing" && !strings.Contains(e.Loc, "_test.") {
			key = e.Key
			break
		}
	}
	if key == "" {
		t.Fatal("gaps lists no undocumented non-test function")
	}
	text := "Acceptance documents this symbol for the end-to-end test."
	a.run(t, "doc", "set", key, "--text", text)
	es := a.json(t, "symbol", key, "--doc-only").entries()
	if len(es) != 1 || !strings.Contains(es[0].Doc, text) || es[0].Extra["doc_state"] != "ok" {
		t.Errorf("symbol %s --doc-only after doc set: %+v", key, es)
	}
}

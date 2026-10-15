package main

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/muralx/codedoc/internal/mcpserver"
	"github.com/muralx/codedoc/internal/session"
)

// discard is a Logger that drops lines.
type discard struct{}

func (discard) Printf(string, ...any) {}

// mcpSession serves the real command tree over an in-memory transport;
// stop closes the session and waits for the server to return.
func mcpSession(t *testing.T) (cs *mcp.ClientSession, stop func()) {
	t.Helper()
	ct, st := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- mcpserver.Serve(ctx, st, mcpOptions(discard{})) }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	var once sync.Once
	stop = func() {
		once.Do(func() {
			_ = cs.Close()
			<-done
			cancel()
		})
	}
	t.Cleanup(stop)
	return cs, stop
}

func mcpCall(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res.Content[0].(*mcp.TextContent).Text, res.IsError
}

// mcpCases pairs each tool call with the CLI arguments that must print the
// same answer. Every tool appears at least once.
var mcpCases = []struct {
	tool string
	in   map[string]any
	cli  []string
}{
	{"codedoc_overview", map[string]any{"repo": "go-lib"}, []string{"overview", "--repo", "go-lib"}},
	{"codedoc_outline", map[string]any{"target": "go-lib:store/store.go"}, []string{"outline", "go-lib:store/store.go"}},
	{"codedoc_outline", map[string]any{"target": "go-lib:store", "detail": true}, []string{"outline", "go-lib:store", "--detail"}},
	{"codedoc_ls", map[string]any{"paths": []string{"go-lib:store", "cfg-repo:*.md"}, "sort": "name", "long": true},
		[]string{"ls", "go-lib:store", "cfg-repo:*.md", "--sort", "name", "--long"}},
	{"codedoc_lines", map[string]any{"path": "go-lib:store/store.go", "range": "26-32"},
		[]string{"lines", "go-lib:store/store.go", "26-32"}},
	{"codedoc_symbol", map[string]any{"symbol": "cfg-repo:README.md#cfg"}, []string{"symbol", "cfg-repo:README.md#cfg"}},
	{"codedoc_find", map[string]any{"pattern": "Get", "kind": "method", "budget": 500},
		[]string{"find", "Get", "--kind", "method", "--budget", "500"}},
	{"codedoc_symbol", map[string]any{"symbol": "MemStore.Get", "no_body": true, "repo": "go-lib"},
		[]string{"symbol", "MemStore.Get", "--no-body", "--repo", "go-lib"}},
	{"codedoc_symbol", map[string]any{"names": []string{"MemStore.Get", "MemStore.Put", "Nope"}},
		[]string{"symbol", "MemStore.Get", "MemStore.Put", "Nope"}},
	{"codedoc_impact", map[string]any{"symbol": "MemStore.Get", "budget": 3000},
		[]string{"impact", "MemStore.Get", "--budget", "3000"}},
	{"codedoc_impact", map[string]any{"symbol": "Store.Get", "only": "impls", "budget": 3000},
		[]string{"impact", "Store.Get", "--only", "impls", "--budget", "3000"}},
	{"codedoc_how", map[string]any{"target": "Service.Lookup"}, []string{"how", "Service.Lookup"}},
	{"codedoc_how", map[string]any{"target": "go-app:main.go"}, []string{"how", "go-app:main.go"}},
	{"codedoc_graph", map[string]any{"relation": "callers", "target": "Service.Lookup", "depth": 2},
		[]string{"callers", "Service.Lookup", "--depth", "2"}},
	{"codedoc_graph", map[string]any{"relation": "impls", "target": "go-lib:store/store.go#Store", "context": "1"},
		[]string{"impls", "go-lib:store/store.go#Store", "--context", "1"}},
	{"codedoc_config", map[string]any{"key": "LIB_MODE"}, []string{"config", "LIB_MODE"}},
	{"codedoc_search", map[string]any{"query": "persist values by key", "kind": "method"},
		[]string{"search", "persist values by key", "--kind", "method"}},
	{"codedoc_explain", map[string]any{"target": "go-lib:store"}, []string{"explain", "go-lib:store"}},
	{"codedoc_flow", map[string]any{"symbol": "Service.Lookup", "depth": 2}, []string{"flow", "Service.Lookup", "--depth", "2"}},
	{"codedoc_history", map[string]any{"mode": "history", "symbol": "MemStore.Get", "repo": "go-lib"},
		[]string{"history", "MemStore.Get", "--repo", "go-lib"}},
	{"codedoc_history", map[string]any{"mode": "at", "symbol": "MemStore.Get", "ref": "HEAD", "repo": "go-lib"},
		[]string{"at", "MemStore.Get", "--ref", "HEAD", "--repo", "go-lib"}},
	{"codedoc_doc", map[string]any{"action": "gaps", "kind": "missing"}, []string{"gaps", "--kind", "missing"}},
	{"codedoc_history", map[string]any{"mode": "changes", "since": "HEAD", "repo": "go-lib"},
		[]string{"changes", "--since", "HEAD", "--repo", "go-lib"}},
	{"codedoc_doc", map[string]any{"action": "check", "repo": "go-lib"}, []string{"doc", "check", "--repo", "go-lib"}},
}

// sameAnswer reports whether a tool result is the answer of the CLI:
// the same lines, but for those naming the command for what the budget
// left out, which name the tool call instead.
func sameAnswer(mcp, cli string) bool {
	got, want := strings.Split(mcp, "\n"), strings.Split(cli, "\n")
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		hint := strings.Contains(want[i], " more") && strings.Contains(want[i], ": codedoc ") && strings.Contains(got[i], ": codedoc_")
		if got[i] != want[i] && !hint {
			return false
		}
	}
	return true
}

func TestMCPToolsAnswerAsTheCLI(t *testing.T) {
	e2eWorkspace(t)
	t.Setenv(session.HeadlessEnv, "1")
	cs, _ := mcpSession(t)
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil || len(tools.Tools) != 19 {
		t.Fatalf("%v %d tools", err, len(tools.Tools))
	}
	for _, c := range mcpCases {
		c.in["workspace"] = "fx"
		got, isErr := mcpCall(t, cs, c.tool, c.in)
		args := append([]string{"--workspace", "fx"}, c.cli...)
		if _, ok := c.in["budget"]; !ok {
			args = append(args, "--budget", "6000")
		}
		want, cliErr := runCLI(t, args...)
		if isErr || cliErr != nil || !sameAnswer(got, want) || got == "" {
			t.Errorf("%s %v: isErr=%v cliErr=%v\nmcp:\n%s\ncli:\n%s", c.tool, c.in, isErr, cliErr, got, want)
		}
	}
}

// An ambiguous address is answered for the best candidate, and one that
// matches nothing with the nearest: both are plain results, as is the
// one line of an address or path nothing can be offered for.
func TestMCPAnswersNearMisses(t *testing.T) {
	e2eWorkspace(t)
	t.Setenv(session.HeadlessEnv, "1")
	cs, _ := mcpSession(t)
	for symbol, want := range map[string][]string{
		"Get":                        {"Store.Get  store/store.go:12\n", "\nalso: MemStore.Get ("},
		"go-lib:store/store.go#Nope": {"== no exact match; nearest: (6)\nstore/store.go\n"},
		"errmissing":                 {"ErrMissing  store/store.go:7\n", "\nanswered for ErrMissing\n"},
		"Zzyzxq":                     {"codedoc: symbol Zzyzxq: not found\n"},
		"go-lib:go.mod/../../x#y":    {"codedoc: go-lib:go.mod/../../x: path is outside the repos of the workspace"},
	} {
		got, isErr := mcpCall(t, cs, "codedoc_symbol", map[string]any{"symbol": symbol, "workspace": "fx"})
		for _, w := range want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: missing %q in\n%s", symbol, w, got)
			}
		}
		if isErr != strings.Contains(symbol, "..") || strings.Contains(got, "codedoc: ") != strings.HasPrefix(got, "codedoc: ") {
			t.Errorf("%s: isError %v\n%s", symbol, isErr, got)
		}
	}
}

func TestMCPUnknownWorkspaceAndWrites(t *testing.T) {
	e2eWorkspace(t)
	t.Setenv(session.HeadlessEnv, "1")
	cs, _ := mcpSession(t)
	got, isErr := mcpCall(t, cs, "codedoc_find", map[string]any{"pattern": "Get", "workspace": "nope"})
	if !isErr || !strings.Contains(got, `unknown workspace "nope"`) {
		t.Fatalf("%v\n%s", isErr, got)
	}
	// A write through the tool answers ok, as the silent CLI prints nothing.
	got, isErr = mcpCall(t, cs, "codedoc_doc", map[string]any{"action": "set", "target": "go-lib:service/",
		"text": "Package service runs use cases.", "workspace": "fx"})
	if isErr || got != "ok" {
		t.Fatalf("%v\n%s", isErr, got)
	}
}

func TestMCPCommandTakesSessionLocks(t *testing.T) {
	repos := e2eWorkspace(t)
	t.Setenv(session.HeadlessEnv, "")
	t.Chdir(repos["go-lib"])
	data := os.Getenv("CODEDOC_DATA")
	_, stop := mcpSession(t)
	for _, r := range []string{"go-lib", "cfg-repo"} {
		if h, err := session.Held(data, "fx", r); !h || err != nil {
			t.Fatalf("%s: %v %v", r, h, err)
		}
	}
	stop()
	if h, err := session.Held(data, "fx", "go-lib"); h || err != nil {
		t.Fatalf("%v %v", h, err)
	}
}

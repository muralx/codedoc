package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/session"
)

// toolHint matches the line of a tool result that names the tool call
// printing what a budget left out.
var toolHint = regexp.MustCompile(`(?m)^(?:\.\.\. )?\d+ more[^\n]*?: (codedoc_[a-z]+) (\{.*\})$`)

// followHint makes the tool call the first omission line of text names
// and returns its answer.
func followHint(t *testing.T, call func(string, map[string]any) string, text string) string {
	t.Helper()
	m := toolHint.FindStringSubmatch(text)
	if m == nil {
		t.Fatalf("no tool call for the rest in\n%s", text)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(m[2]), &args); err != nil {
		t.Fatalf("%s: %v", m[2], err)
	}
	return call(m[1], args)
}

// hintCaller serves the e2e workspace over MCP and returns a function
// that makes a tool call and fails on an error result or on an answer
// with a hint only a shell can follow.
func hintCaller(t *testing.T) func(string, map[string]any) string {
	t.Helper()
	e2eWorkspace(t)
	t.Setenv(session.HeadlessEnv, "1")
	cs, _ := mcpSession(t)
	return func(tool string, args map[string]any) string {
		t.Helper()
		got, isErr := mcpCall(t, cs, tool, args)
		if isErr || strings.Contains(got, "--out") || strings.Contains(got, "--offset") || strings.Contains(got, "--budget") {
			t.Fatalf("%s %v: isErr=%v\n%s", tool, args, isErr, got)
		}
		return got
	}
}

// An answer cut by the budget tells an MCP caller the tool call that
// prints the rest: its inputs, never a shell command or --out FILE.
func TestMCPOmissionLinesNameToolCalls(t *testing.T) {
	call := hintCaller(t)
	// A list: the same tool at the next offset.
	first := call("codedoc_find", map[string]any{"pattern": "*Store*", "workspace": "fx", "budget": 300})
	if !strings.Contains(first, ` more: codedoc_find {"pattern":"*Store*","budget":300,"workspace":"fx","offset":`) {
		t.Fatalf("got\n%s", first)
	}
	next := followHint(t, call, first)
	if !strings.Contains(next, "== find *Store* (") || next == first {
		t.Fatalf("the call for the rest answers\n%s", next)
	}
	// A source: codedoc_lines with the path and the range left.
	first = call("codedoc_symbol", map[string]any{"symbol": "Service.Lookup", "workspace": "fx", "budget": 150})
	if !strings.Contains(first, ` more lines: codedoc_lines {"path":"go-lib:service/service.go","range":"16-23","workspace":"fx"}`) {
		t.Fatalf("got\n%s", first)
	}
	if next = followHint(t, call, first); !strings.Contains(next, "\n23\t}\n") {
		t.Fatalf("the call for the rest answers\n%s", next)
	}
}

// The same when not one entry fits, and for one section of several.
func TestMCPOmissionLinesNameTheBudgetOrTheSection(t *testing.T) {
	call := hintCaller(t)
	// Not one entry fits: the same tool with the budget that does.
	first := call("codedoc_find", map[string]any{"pattern": "*Store*", "workspace": "fx", "budget": 160})
	next := followHint(t, call, first)
	if !strings.Contains(next, "NullStore  ") {
		t.Fatalf("got\n%s\nthen\n%s", first, next)
	}
	// A section of an impact answer: the tool that lists it alone.
	first = call("codedoc_impact", map[string]any{"symbol": "Store.Get", "workspace": "fx", "budget": 400})
	if next = followHint(t, call, first); strings.Contains(next, "not found") || next == "" {
		t.Fatalf("got\n%s\nthen\n%s", first, next)
	}
}

// codedoc_lines cut by the budget continues by the tool call its last
// line names. The JSON of the call may pass the budget by its own
// bytes: the lines of the file never do.
func TestMCPLinesContinueByToolCall(t *testing.T) {
	repos := e2eWorkspace(t)
	t.Setenv(session.HeadlessEnv, "1")
	src, err := os.ReadFile(filepath.Join(repos["go-lib"], "store", "store.go"))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Split(strings.TrimSuffix(string(src), "\n"), "\n")
	t.Chdir(t.TempDir()) // outside every repo, as an agent may be
	cs, _ := mcpSession(t)
	text, isErr := mcpCall(t, cs, "codedoc_lines", map[string]any{"path": "go-lib:store/store.go", "range": "1-999", "budget": 400,
		"workspace": "fx"})
	m := toolHint.FindStringSubmatch(text)
	if isErr || m == nil || len(text) > 400+len(m[2]) || !strings.Contains(text, "\n1\t"+want[0]+"\n") {
		t.Fatalf("%v, %d bytes\n%s", isErr, len(text), text)
	}
	next := followHint(t, func(tool string, args map[string]any) string {
		got, _ := mcpCall(t, cs, tool, args)
		return got
	}, text)
	if !strings.Contains(next, "\n10\t"+want[9]+"\n") || strings.Contains(next, "\n9\t") {
		t.Fatalf("the call for the rest answers\n%s", next)
	}
}

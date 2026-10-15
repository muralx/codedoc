package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The command an omission line names is, for an MCP caller, the tool
// and the inputs that print the rest.
func TestToolCallTranslatesTheCommandsAnswersName(t *testing.T) {
	for command, want := range map[string]string{
		"codedoc find '*Store*' --budget 400 --offset 3":      `codedoc_find {"pattern":"*Store*","budget":400,"offset":3}`,
		"codedoc lines go-lib:service/service.go 16-23":       `codedoc_lines {"path":"go-lib:service/service.go","range":"16-23"}`,
		"codedoc lines 'docs/with space & more.md' 23-308":    `codedoc_lines {"path":"docs/with space & more.md","range":"23-308"}`,
		"codedoc symbol 'it'\\''s' --no-body --budget 900":    `codedoc_symbol {"symbol":"it's","no_body":true,"budget":900}`,
		"codedoc symbol A B --budget 700":                     `codedoc_symbol {"names":["A","B"],"budget":700}`,
		"codedoc callers Store.Get --workspace fx --offset 4": `codedoc_graph {"relation":"callers","target":"Store.Get","workspace":"fx","offset":4}`,
		"codedoc impact Store.Get --only impls":               `codedoc_impact {"symbol":"Store.Get","only":"impls"}`,
		"codedoc tests Store.Get --transitive --offset 9":     `codedoc_impact {"symbol":"Store.Get","offset":9,"only":"tests"}`,
		"codedoc flow 'go-app:main.go#main' --depth 2":        `codedoc_flow {"symbol":"go-app:main.go#main","depth":2}`,
		"codedoc where 'wait longer' --limit 2 --offset 1":    `codedoc_where {"query":"wait longer","limit":2,"offset":1}`,
		"codedoc outline @acme/sdk@1.2.0 --offset 3":          `codedoc_outline {"target":"@acme/sdk@1.2.0","offset":3}`,
		"codedoc api @acme/sdk Server --budget=500":           `codedoc_api {"package":"@acme/sdk","name":"Server","budget":500}`,
		"codedoc search retry --context symbol --offset 20":   `codedoc_search {"query":"retry","context":"symbol","offset":20}`,
		"codedoc ls docs 'src/**/*.ts' --long --offset 40":    `codedoc_ls {"paths":["docs","src/**/*.ts"],"long":true,"offset":40}`,
		"codedoc deps --offset 25":                            `codedoc_deps {"offset":25}`,
		"codedoc deps github.com/fixture/app --offset 2":      `codedoc_graph {"relation":"deps","target":"github.com/fixture/app","offset":2}`,
		"codedoc changes --since HEAD --offset 5":             `codedoc_history {"mode":"changes","since":"HEAD","offset":5}`,
		"codedoc gaps --offset 20":                            `codedoc_doc {"action":"gaps","offset":20}`,
		"codedoc feedback --list --offset 20":                 `codedoc_feedback {"list":true,"offset":20}`,
	} {
		if got, ok := toolCall(command, ""); !ok || got != want {
			t.Errorf("%s:\n got %s (%v)\nwant %s", command, got, ok, want)
		}
	}
	// No tool runs these with the same meaning: they stay as they are.
	for _, command := range []string{"codedoc find Get --lang go --offset 3", "codedoc doc check", "codedoc lines f.go 1-2 5-6",
		"codedoc", "grep -r x", "codedoc tests X --transitive --kind type"} {
		if got, ok := toolCall(command, ""); ok {
			t.Errorf("%s: translated to %s", command, got)
		}
	}
}

// Only omission lines are rewritten: the lines of a file, an entry and
// a note are the answer and stay as they are.
func TestToolHintsRewriteOmissionLinesOnly(t *testing.T) {
	in := "go-lib@0a93839\n== find *Store* (11)\n" +
		"NullStore  store/null.go:4  type NullStore struct\n" +
		"12\t// 3 more: codedoc find x --offset 1\n" +
		"  7 more call sites in this file: codedoc callers Store.Get --offset 3\n" +
		"8 more lines: codedoc lines go-lib:store/store.go 31-38\n" +
		"8 more: codedoc find '*Store*' --budget 400 --offset 3\n" +
		"... 2 more sections (explain go-app:main.go, tests): codedoc how go-app:main.go --budget 2100\n" +
		"3 more: codedoc find Get --lang go --offset 3\n" +
		"Store is 7 lines, over the budget: its members are listed; codedoc symbol Store.<member> prints one\n"
	want := "go-lib@0a93839\n== find *Store* (11)\n" +
		"NullStore  store/null.go:4  type NullStore struct\n" +
		"12\t// 3 more: codedoc find x --offset 1\n" +
		`  7 more call sites in this file: codedoc_graph {"relation":"callers","target":"Store.Get","offset":3}` + "\n" +
		`8 more lines: codedoc_lines {"path":"go-lib:store/store.go","range":"31-38"}` + "\n" +
		`8 more: codedoc_find {"pattern":"*Store*","budget":400,"offset":3}` + "\n" +
		`... 2 more sections (explain go-app:main.go, tests): codedoc_how {"target":"go-app:main.go","budget":2100}` + "\n" +
		"3 more: codedoc find Get --lang go --offset 3\n" +
		"Store is 7 lines, over the budget: its members are listed; codedoc symbol Store.<member> prints one\n"
	if got := toolHints(in, ""); got != want {
		t.Fatalf("got\n%s", got)
	}
	// The workspace of the call goes with a call that names none.
	if got, _ := toolCall("codedoc lines go-lib:a.go 3-9", "fx"); got != `codedoc_lines {"path":"go-lib:a.go","range":"3-9","workspace":"fx"}` {
		t.Fatalf("got %s", got)
	}
	if got, _ := toolCall("codedoc find G --workspace w --offset 2", "fx"); got != `codedoc_find {"pattern":"G","workspace":"w","offset":2}` {
		t.Fatalf("got %s", got)
	}
}

// A tool result carries the rewritten lines, and never a hint only a
// shell can follow.
func TestToolResultNamesToolInputsForTheRest(t *testing.T) {
	rec := &recorder{stdout: "== find G (9)\nGet  a.go:1\n8 more: codedoc find G --budget 100 --offset 1\n"}
	cs, _ := connect(t, Options{Exec: rec.exec, Version: "t", Log: &lines{}})
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "codedoc_find",
		Arguments: map[string]any{"pattern": "G", "budget": 100}})
	if err != nil {
		t.Fatal(err)
	}
	got := res.Content[0].(*mcp.TextContent).Text
	if !strings.HasSuffix(got, "Get  a.go:1\n8 more: codedoc_find {\"pattern\":\"G\",\"budget\":100,\"offset\":1}\n") ||
		strings.Contains(got, "--") {
		t.Fatalf("got\n%s", got)
	}
}

package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/muralx/codedoc/internal/query"
	"github.com/muralx/codedoc/internal/session"
)

// lines is a Logger that keeps what it is given.
type lines struct {
	mu  sync.Mutex
	all []string
}

func (l *lines) Printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.all = append(l.all, fmt.Sprintf(format, args...))
}

func (l *lines) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.all, "\n")
}

// recorder is an Executor that records argv and answers with canned
// output and error.
type recorder struct {
	args           [][]string
	stdin          []string // what each call was given on stdin
	stdout, stderr string
	err            error
}

func (r *recorder) exec(_ context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	r.args = append(r.args, args)
	in, _ := io.ReadAll(stdin)
	r.stdin = append(r.stdin, string(in))
	_, _ = io.WriteString(stdout, r.stdout)
	_, _ = io.WriteString(stderr, r.stderr)
	return r.err
}

// connect serves opts over an in-memory transport and returns a client
// session; stop closes it and waits for Serve to return.
func connect(t *testing.T, opts Options) (cs *mcp.ClientSession, stop func()) {
	t.Helper()
	ct, st := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, st, opts) }()
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

func callText(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		b.WriteString(c.(*mcp.TextContent).Text)
	}
	return b.String(), res.IsError
}

func headless(t *testing.T) {
	t.Helper()
	t.Setenv(session.HeadlessEnv, "1")
}

// toolNames is the list of tools the server registers, sorted: the one
// place the tests name them all.
var toolNames = []string{"codedoc_api", "codedoc_config", "codedoc_deps", "codedoc_doc", "codedoc_explain",
	"codedoc_feedback", "codedoc_find", "codedoc_flow", "codedoc_graph", "codedoc_history", "codedoc_how", "codedoc_impact",
	"codedoc_lines", "codedoc_ls", "codedoc_outline", "codedoc_overview", "codedoc_search", "codedoc_symbol", "codedoc_where"}

// inputsOf maps each tool to the inputs it has besides the common four;
// the flags an agent seldom needs stay on the CLI (efficiency spec
// section 9).
var inputsOf = map[string]string{
	"codedoc_api":      "detail name package",
	"codedoc_config":   "key",
	"codedoc_deps":     "action",
	"codedoc_doc":      "action docs kind target text",
	"codedoc_explain":  "target",
	"codedoc_feedback": "list path text",
	"codedoc_find":     "deps detail kind pattern",
	"codedoc_flow":     "depth symbol",
	"codedoc_graph":    "context depth detail no_tests relation target",
	"codedoc_history":  "diff mode ref since symbol",
	"codedoc_how":      "target",
	"codedoc_impact":   "context detail only symbol",
	"codedoc_lines":    "path range",
	"codedoc_ls":       "long paths since sort",
	"codedoc_outline":  "detail target",
	"codedoc_overview": "",
	"codedoc_search":   "context deps detail kind query",
	"codedoc_symbol":   "deps detail names no_body symbol",
	"codedoc_where":    "context deps limit query",
}

// listTools returns the tools a client is told about.
func listTools(t *testing.T) []*mcp.Tool {
	t.Helper()
	headless(t)
	cs, _ := connect(t, Options{Exec: (&recorder{}).exec, Log: &lines{}})
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return res.Tools
}

// ownInputs returns the inputs of a tool besides the common four,
// sorted and joined by spaces; it fails when one of the common is gone.
func ownInputs(t *testing.T, tl *mcp.Tool) string {
	t.Helper()
	raw, err := json.Marshal(tl.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	var own []string
	for name := range schema.Properties {
		if !slices.Contains([]string{"workspace", "repo", "budget", "offset"}, name) {
			own = append(own, name)
		}
	}
	if len(schema.Properties) != len(own)+4 {
		t.Errorf("%s: a common input is missing: %s", tl.Name, raw)
	}
	slices.Sort(own)
	return strings.Join(own, " ")
}

func TestListToolsNamesAndSchemas(t *testing.T) {
	tools := listTools(t)
	var names []string
	for _, tl := range tools {
		names = append(names, tl.Name)
		if got := ownInputs(t, tl); got != inputsOf[tl.Name] {
			t.Errorf("%s: inputs %q, want %q beside the common four", tl.Name, got, inputsOf[tl.Name])
		}
		// One sentence each: the descriptions are paid for in every request
		// of a client that does not defer its tools.
		if d := tl.Description; !strings.HasSuffix(d, ".") || strings.Contains(d, ". ") || len(d) > 130 {
			t.Errorf("%s: description is not one short sentence: %s", tl.Name, d)
		}
	}
	slices.Sort(names)
	if !slices.Equal(names, toolNames) {
		t.Fatalf("got %v", names)
	}
}

func TestToolSchemaEnums(t *testing.T) {
	want := map[string][]string{
		"codedoc_ls":     {"enum:[mtime committed name size lines]"},
		"codedoc_graph":  {"required:[relation target]", "dependents]"},
		"codedoc_deps":   {"enum:[list index prune]"},
		"codedoc_doc":    {"enum:[gaps set check]", "enum:[missing stale module]"},
		"codedoc_impact": {"enum:[callers refs impls tests mentions config]"},
	}
	for _, tl := range listTools(t) {
		schema := fmt.Sprint(tl.InputSchema)
		for _, w := range want[tl.Name] {
			if !strings.Contains(schema, w) {
				t.Errorf("%s: schema lacks %s: %s", tl.Name, w, schema)
			}
		}
	}
}

func TestToolArgv(t *testing.T) {
	headless(t)
	rec := &recorder{stdout: "answer\n"}
	cs, _ := connect(t, Options{Exec: rec.exec, Log: &lines{}})
	for _, c := range argvCases {
		rec.args = nil
		text, isErr := callText(t, cs, c.tool, c.in)
		if isErr || text != "answer\n" || len(rec.args) != 1 || strings.Join(rec.args[0], " ") != c.want {
			t.Errorf("%s %v: err=%v %q\n got  %q\n want %q", c.tool, c.in, isErr, text, rec.args, c.want)
		}
	}
}

var argvCases = []struct {
	tool string
	in   map[string]any
	want string
}{
	{"codedoc_overview", nil, "overview --budget=6000"},
	{"codedoc_outline", map[string]any{"target": "-odd.go", "workspace": "fx", "repo": "go-lib", "budget": 0, "offset": 2},
		"outline --workspace=fx --repo=go-lib --budget=60000 --offset=2 -- -odd.go"},
	{"codedoc_outline", map[string]any{"target": "src", "detail": true}, "outline --detail --budget=6000 -- src"},
	{"codedoc_ls", nil, "ls --budget=6000"},
	{"codedoc_ls", map[string]any{"paths": []string{"docs", "src/**/*.ts"}, "sort": "lines", "since": "2d", "long": true, "repo": "r"},
		"ls --sort=lines --since=2d --long --repo=r --budget=6000 -- docs src/**/*.ts"},
	{"codedoc_feedback", map[string]any{"text": "body ends early", "path": "a.go", "workspace": "fx", "repo": "r"},
		"feedback --path=a.go --workspace=fx --repo=r --budget=6000 -- body ends early"},
	{"codedoc_feedback", map[string]any{"list": true, "offset": 20, "budget": 900}, "feedback --list --budget=900 --offset=20"},
	{"codedoc_lines", map[string]any{"path": "go-lib:store/store.go", "range": "26+6", "repo": "r"},
		"lines --repo=r --budget=6000 -- go-lib:store/store.go 26+6"},
	{"codedoc_find", map[string]any{"pattern": "Get", "kind": "method", "detail": true},
		"find --detail --kind=method --budget=6000 -- Get"},
	{"codedoc_find", map[string]any{"pattern": "Server", "deps": true}, "find --deps --budget=6000 -- Server"},
	{"codedoc_symbol", map[string]any{"symbol": "MemStore.Get"}, "symbol --budget=6000 -- MemStore.Get"},
	{"codedoc_symbol", map[string]any{"symbol": "S", "no_body": true, "detail": true},
		"symbol --detail --no-body --budget=6000 -- S"},
	{"codedoc_symbol", map[string]any{"names": []string{"A.b", "C", "pkg/x.go:12"}},
		"symbol --budget=6000 -- A.b C pkg/x.go:12"},
	{"codedoc_symbol", map[string]any{"symbol": "Server.notification", "names": []string{"Server"}, "deps": true},
		"symbol --deps --budget=6000 -- Server.notification Server"},
	{"codedoc_impact", map[string]any{"symbol": "MemStore.Get"}, "impact --budget=3000 -- MemStore.Get"},
	{"codedoc_impact", map[string]any{"symbol": "X", "only": "mentions", "offset": 5, "context": "2", "detail": true,
		"budget": 9000}, "impact --only=mentions --context=2 --detail --budget=9000 --offset=5 -- X"},
	{"codedoc_how", map[string]any{"target": "go-app:main.go", "workspace": "fx"},
		"how --workspace=fx --budget=6000 -- go-app:main.go"},
	{"codedoc_api", map[string]any{"package": "@acme/sdk"}, "api --budget=6000 -- @acme/sdk"},
	{"codedoc_api", map[string]any{"package": "@acme/sdk@1.2.0", "name": "Server", "detail": true},
		"api --detail --budget=6000 -- @acme/sdk@1.2.0 Server"},
	{"codedoc_graph", map[string]any{"relation": "callers", "target": "X", "depth": 2, "no_tests": true, "context": "symbol"},
		"callers --depth=2 --no-tests --context=symbol --budget=6000 -- X"},
	{"codedoc_graph", map[string]any{"relation": "tests", "target": "X", "detail": true}, "tests --detail --budget=6000 -- X"},
	{"codedoc_config", map[string]any{"key": "LIB_MODE"}, "config --budget=6000 -- LIB_MODE"},
	{"codedoc_search", map[string]any{"query": "persist values", "kind": "module", "context": "3", "detail": true},
		"search --kind=module --context=3 --detail --budget=6000 -- persist values"},
	{"codedoc_search", map[string]any{"query": "send a notification", "deps": true},
		"search --deps --budget=6000 -- send a notification"},
	{"codedoc_where", map[string]any{"query": "retry a delivery", "limit": 3, "context": "symbol", "deps": true},
		"where --deps --limit=3 --context=symbol --budget=6000 -- retry a delivery"},
	{"codedoc_explain", map[string]any{"target": "go-lib:store"}, "explain --budget=6000 -- go-lib:store"},
	{"codedoc_flow", map[string]any{"symbol": "main", "depth": 4}, "flow --depth=4 --budget=6000 -- main"},
	{"codedoc_history", map[string]any{"mode": "changes", "since": "HEAD"}, "changes --since=HEAD --budget=6000"},
	{"codedoc_history", map[string]any{"mode": "history", "symbol": "S"}, "history --budget=6000 -- S"},
	{"codedoc_history", map[string]any{"mode": "at", "symbol": "S", "ref": "HEAD~1", "diff": "true"},
		"at --ref=HEAD~1 --diff --budget=6000 -- S"},
	{"codedoc_history", map[string]any{"mode": "at", "symbol": "S", "ref": "v1", "diff": "HEAD"},
		"at --ref=v1 --diff=HEAD --budget=6000 -- S"},
	{"codedoc_doc", map[string]any{"action": "gaps", "kind": "stale", "target": "m"}, "gaps --kind=stale --budget=6000 -- m"},
	{"codedoc_doc", map[string]any{"action": "gaps"}, "gaps --budget=6000"},
	{"codedoc_doc", map[string]any{"action": "set", "target": "S", "text": "S does it.\n\nMore."},
		"doc set --text=S does it.\n\nMore. --budget=6000 -- S"},
	{"codedoc_doc", map[string]any{"action": "check"}, "doc check --budget=6000"},
	{"codedoc_deps", nil, "deps --budget=6000"},
	{"codedoc_deps", map[string]any{"action": "list", "repo": "web"}, "deps --repo=web --budget=6000"},
	{"codedoc_deps", map[string]any{"action": "index", "workspace": "fx"}, "deps index --workspace=fx --budget=6000"},
	{"codedoc_deps", map[string]any{"action": "prune"}, "deps prune --budget=6000"},
}

func TestMissingInputsAreToolErrors(t *testing.T) {
	headless(t)
	rec := &recorder{}
	cs, _ := connect(t, Options{Exec: rec.exec, Log: &lines{}})
	for _, c := range []struct {
		tool string
		in   map[string]any
		want string
	}{
		{"codedoc_outline", map[string]any{"target": ""}, "input target is required"},
		{"codedoc_find", map[string]any{"pattern": ""}, "input pattern is required"},
		{"codedoc_lines", map[string]any{"path": "", "range": "1-2"}, "input path is required"},
		{"codedoc_lines", map[string]any{"path": "a.go", "range": ""}, "input range is required"},
		{"codedoc_symbol", map[string]any{"symbol": ""}, "input symbol is required unless names is set"},
		{"codedoc_impact", map[string]any{"symbol": ""}, "input symbol is required"},
		{"codedoc_how", map[string]any{"target": ""}, "input target is required"},
		{"codedoc_api", map[string]any{"package": "", "name": "Server"}, "input package is required"},
		{"codedoc_feedback", map[string]any{"path": "a.go"}, "input text is required unless list is set"},
		{"codedoc_graph", map[string]any{"relation": "callers", "target": ""}, "input target is required"},
		{"codedoc_config", map[string]any{"key": ""}, "input key is required"},
		{"codedoc_search", map[string]any{"query": ""}, "input query is required"},
		{"codedoc_where", map[string]any{"query": ""}, "input query is required"},
		{"codedoc_explain", map[string]any{"target": ""}, "input target is required"},
		{"codedoc_flow", map[string]any{"symbol": ""}, "input symbol is required"},
		{"codedoc_history", map[string]any{"mode": "changes"}, "input since is required for mode changes"},
		{"codedoc_history", map[string]any{"mode": "history"}, "input symbol is required for mode history"},
		{"codedoc_history", map[string]any{"mode": "at", "ref": "v1"}, "input symbol is required for mode at"},
		{"codedoc_history", map[string]any{"mode": "at", "symbol": "S"}, "input ref is required for mode at"},
		{"codedoc_doc", map[string]any{"action": "set", "text": "x"}, "input target is required for action set"},
		{"codedoc_doc", map[string]any{"action": "set", "target": "S"}, "input text is required for action set"},
		{"codedoc_deps", map[string]any{"action": "prune", "repo": "web"},
			"inputs workspace and repo do not apply to action prune: it covers every workspace of the config"},
	} {
		text, isErr := callText(t, cs, c.tool, c.in)
		if !isErr || text != c.want {
			t.Errorf("%s %v: %v %q", c.tool, c.in, isErr, text)
		}
	}
	if len(rec.args) != 0 {
		t.Fatalf("executed %v", rec.args)
	}
}

func TestEnumsRejectUnknownValues(t *testing.T) {
	headless(t)
	cs, _ := connect(t, Options{Exec: (&recorder{}).exec, Log: &lines{}})
	// An unknown value of an enumeration is refused, and so is an input
	// the schema no longer has (format, out and the like stay on the CLI).
	for tool, in := range map[string]map[string]any{
		"codedoc_history": {"mode": "blame"},
		"codedoc_impact":  {"symbol": "X", "only": "everything"},
		"codedoc_find":    {"pattern": "X", "format": "json"},
		"codedoc_symbol":  {"symbol": "X", "out": "true"},
	} {
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: in})
		if err == nil && !res.IsError {
			t.Fatalf("%s %v accepted", tool, in)
		}
	}
	for _, in := range []input{HistoryInput{Mode: "blame"}, DocInput{Action: "drop"}, DepsInput{Action: "drop"}} {
		if err := in.build(&argv{}); err == nil || !strings.Contains(err.Error(), "want") {
			t.Fatalf("%v: %v", in, err)
		}
	}
}

func TestResultsForErrors(t *testing.T) {
	headless(t)
	log := &lines{}
	rec := &recorder{stdout: "candidates\n", stderr: "warn\n"}
	cs, _ := connect(t, Options{Exec: rec.exec, Log: log})
	if text, isErr := callText(t, cs, "codedoc_overview", nil); isErr || text != "candidates\n" ||
		!strings.Contains(log.String(), "codedoc_overview: warn") {
		t.Fatalf("%v %q %q", isErr, text, log.String())
	}
	for _, c := range []struct {
		err   error
		isErr bool
	}{
		{fmt.Errorf("symbol X: %w", query.ErrAmbiguous), false},
		{fmt.Errorf("symbol X: %w", query.ErrNotFound), false},
		{errors.New("unknown workspace \"q\""), true},
	} {
		rec.err = c.err
		text, isErr := callText(t, cs, "codedoc_symbol", map[string]any{"symbol": "X"})
		if isErr != c.isErr || text != "candidates\nwarn\ncodedoc: "+c.err.Error()+"\n" {
			t.Fatalf("%v: %v %q", c.err, isErr, text)
		}
	}
}

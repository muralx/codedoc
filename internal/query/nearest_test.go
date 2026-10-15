package query_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/query"
	"github.com/muralx/codedoc/internal/testutil"
)

// linkFiles is a TypeScript repo shaped like the one of the usage study
// (efficiency spec section 1): two bin.ts, a tools.ts that declares no
// registerTool, and register declared by two classes and a test helper.
var linkFiles = map[string]string{
	"package.json": `{"name": "link", "private": true}` + "\n",
	".gitignore":   "out/\n*.log\n",
	"packages/edge/src/bin.ts": "/** readStdin collects all of stdin. */\nasync function readStdin(): Promise<string> {\n  return \"\";\n}\n\n" +
		"/** hook runs the named hook. */\nasync function hook(event: string): Promise<void> {\n  await readStdin();\n}\n",
	"packages/hub/src/bin.ts": "/** main runs the hub. */\nasync function main(): Promise<void> {}\n",
	"packages/edge/src/cli.ts": "/** parse reads the arguments. */\nfunction parse(): string[] {\n  return [];\n}\n\n" +
		"/** runMain starts the command line. */\nfunction runMain(): void {\n  parse();\n}\n",
	"packages/edge/src/tools.ts": "/** ToolContext is what handlers receive. */\nexport interface ToolContext {\n  cwd: string;\n}\n\n" +
		"/** summarise wraps a list. */\nfunction summarise(n: number): number {\n  return n;\n}\n\n" +
		"/** callTool runs one tool. */\nexport function callTool(name: string): number {\n  return summarise(name.length);\n}\n",
	"packages/hub/src/store/agents.ts": "/** AgentStore keeps the agents. */\nexport class AgentStore {\n" +
		"  /** register creates an online agent. */\n  register(name: string): string {\n    return name;\n  }\n}\n",
	"packages/edge/src/hub-client.ts": "/** HubClient talks to the hub. */\nexport class HubClient {\n" +
		"  /** register asks the hub for an identity. */\n  register(name: string): string {\n    return name;\n  }\n}\n",
	"packages/hub/src/routes.ts": "import { AgentStore } from \"./store/agents\";\n\n/** routes wires the agent routes. */\n" +
		"export function routes(): string {\n  const agents = new AgentStore();\n  agents.register(\"a\");\n  return agents.register(\"b\");\n}\n",
	"packages/hub/src/server.test.ts": "async function register(base: string): Promise<string> {\n  return base;\n}\n\n" +
		"test(\"registers\", async () => {\n  await register(\"x\");\n});\n",
	"docs/delivery.md": "# Delivery\n\nA message is delivered through the channel; the hook nags when it stays unread.\n",
}

// linkWorkspace indexes linkFiles as the repo link, the current repo,
// with the working directory at its root.
func linkWorkspace(t *testing.T) *query.Ctx {
	t.Helper()
	root := testutil.SourceRepo(t, "link", linkFiles)
	c := indexRepos(t, &config.Repo{Name: "link", Path: root})
	c.Repo, c.Cwd = c.Workspace.Repos[0], root
	return c
}

// nearest renders an answer that must be the nearest candidates.
func nearest(t *testing.T, r *output.Response, err error) string {
	t.Helper()
	if !errors.Is(err, query.ErrNearest) || !errors.Is(err, query.ErrNotFound) || r == nil {
		t.Fatalf("want the nearest candidates, got %v, %v", err, r)
	}
	return render(t, r)
}

// The two not-found answers of the study: a path form whose file
// declares no such symbol is answered with the symbols of the file, in
// one call; a path that is only a file name stands for the files with
// that name.
func TestPathFormWithoutThatSymbolAnswersWithTheFile(t *testing.T) {
	c := linkWorkspace(t)
	got := nearest(t, must(query.Symbol(c, "packages/edge/src/bin.ts#main", query.SymbolOpts{})).resp,
		must(query.Symbol(c, "packages/edge/src/bin.ts#main", query.SymbolOpts{})).err)
	wants(t, got, "== no exact match; nearest: (2)\npackages/edge/src/bin.ts\n  readStdin  :2-4  async function readStdin(): Promise<string>",
		"  hook  :7-9  ")
	// The names nearest to the one asked for lead: they share a word.
	r, err := query.Symbol(c, "packages/edge/src/tools.ts#registerTool", query.SymbolOpts{})
	got = nearest(t, r, err)
	wants(t, got, "== no exact match; nearest: (3)\npackages/edge/src/tools.ts\n  ToolContext  :2-4  ", "  callTool  :12-14  ")
	if strings.Index(got, "callTool") > strings.Index(got, "summarise") {
		t.Errorf("the names sharing a word with registerTool lead:\n%s", got)
	}
	// A bare file name: the only bin.ts that declares main is answered.
	r, err = query.Symbol(c, "bin.ts#main", query.SymbolOpts{})
	wants(t, text(t, r, err), "main  packages/hub/src/bin.ts:2\n1\t/** main runs the hub. */\n", "answered for main\n")
	r, err = query.Symbol(c, "tools.ts#registerTool", query.SymbolOpts{})
	wants(t, nearest(t, r, err), "== no exact match; nearest: (3)\npackages/edge/src/tools.ts\n")
	// The only symbol of the file whose name holds the one asked for.
	r, err = query.Symbol(c, "packages/edge/src/cli.ts#main", query.SymbolOpts{})
	wants(t, text(t, r, err), "runMain  packages/edge/src/cli.ts:7-9\n", "answered for runMain\n")
	// A line no file with that name... a line of the only tools.ts.
	r, err = query.Symbol(c, "tools.ts:13", query.SymbolOpts{NoBody: true})
	wants(t, text(t, r, err), "callTool  packages/edge/src/tools.ts:12-14  ", "answered for callTool")
	// A file path alone is not a symbol: its symbols are the answer.
	r, err = query.Symbol(c, "packages/edge/src/cli.ts", query.SymbolOpts{})
	wants(t, nearest(t, r, err), "packages/edge/src/cli.ts\n  parse  :2-4  ", "  runMain  :7-9  ")
}

// Every command that resolves an address answers the same way.
func TestEveryAddressCommandAnswersANearMiss(t *testing.T) {
	c := linkWorkspace(t)
	for name, call := range map[string]func(addr string) (*output.Response, error){
		"callers":    func(a string) (*output.Response, error) { return query.Callers(c, a, query.GraphOpts{Depth: 1}) },
		"callees":    func(a string) (*output.Response, error) { return query.Callees(c, a, query.GraphOpts{Depth: 1}) },
		"refs":       func(a string) (*output.Response, error) { return query.Refs(c, a, query.GraphOpts{}) },
		"impls":      func(a string) (*output.Response, error) { return query.Impls(c, a, query.GraphOpts{}) },
		"supertypes": func(a string) (*output.Response, error) { return query.Supertypes(c, a, query.GraphOpts{}) },
		"tests":      func(a string) (*output.Response, error) { return query.Tests(c, a, query.GraphOpts{}) },
		"explain":    func(a string) (*output.Response, error) { return query.Explain(c, a) },
		"flow":       func(a string) (*output.Response, error) { return query.Flow(c, a, query.FlowOpts{Depth: 1}) },
		"siblings":   func(a string) (*output.Response, error) { return query.Siblings(c, a) },
		"history":    func(a string) (*output.Response, error) { return query.History(c, a, 0) },
	} {
		r, err := call("packages/edge/src/bin.ts#main")
		if got := nearest(t, r, err); !strings.Contains(got, "== no exact match; nearest: (2)\npackages/edge/src/bin.ts\n") {
			t.Errorf("%s:\n%s", name, got)
		}
		r, err = call("agentstore")
		if err != nil || !strings.Contains(render(t, r), "answered for AgentStore") {
			t.Errorf("%s agentstore: %v\n%v", name, err, r)
		}
		if r, err = call("zzyzxq"); !errors.Is(err, query.ErrNotFound) || errors.Is(err, query.ErrNearest) || r != nil {
			t.Errorf("%s of nothing: %v, %v", name, err, r)
		}
	}
}

func TestBareNameThatMatchesNothing(t *testing.T) {
	c := linkWorkspace(t)
	for addr, want := range map[string]string{
		"hubclient":            "HubClient", // ignoring case
		"HUBCLIENT.REGISTER":   "HubClient.register",
		"Store.register":       "AgentStore.register", // the only name ending with it
		"Client":               "HubClient",
		"Nope.runMain":         "runMain", // the name, whatever the container
		"link:ts:link:x/hook":  "hook",    // the name of a key that names nothing
		"nope/nope.ts#runMain": "runMain", // the name of a path form no file answers
	} {
		r, err := query.Symbol(c, addr, query.SymbolOpts{NoBody: true})
		if got := text(t, r, err); !strings.HasPrefix(got, want+"  ") || !strings.HasSuffix(got, "answered for "+want+"\n") {
			t.Errorf("%s:\n%s", addr, got)
		}
	}
	// Several plausible names, a part of a name and a typo are listed,
	// and only they: no full-text hit follows candidates found by name.
	r, err := query.Symbol(c, "REGISTER", query.SymbolOpts{})
	wants(t, nearest(t, r, err), "== no exact match; nearest: (4)\nHubClient.register  ", "\nAgentStore.register  ",
		"\n  register  :1-3  async function register(base: string): Promise<string>  [test]\n")
	r, err = query.Symbol(c, "Stdin", query.SymbolOpts{})
	wants(t, text(t, r, err), "answered for readStdin")
	r, err = query.Symbol(c, "agent", query.SymbolOpts{})
	wants(t, nearest(t, r, err), "== no exact match; nearest: (1)\nAgentStore  packages/hub/src/store/agents.ts:2-7  export class AgentStore\n")
	r, err = query.Symbol(c, "sumarise", query.SymbolOpts{})
	wants(t, nearest(t, r, err), "== no exact match; nearest: (1)\nsummarise  packages/edge/src/tools.ts:7-9  ")
	// Nothing near by name: the full-text hits.
	r, err = query.Symbol(c, "unread", query.SymbolOpts{})
	wants(t, nearest(t, r, err), "== no exact match; nearest: (1)\nDelivery  docs/delivery.md:1-3  # Delivery\n")
	// A full-text hit must hold every word of the address as a whole
	// word: one that shares a word of several is no reading of it.
	if r, err = query.Symbol(c, "unread.zzyzxq", query.SymbolOpts{}); r != nil || !errors.Is(err, query.ErrNotFound) {
		t.Errorf("a hit for one word of two: %v, %v", err, r)
	}
	if r, err = query.Symbol(c, "unrea", query.SymbolOpts{}); r != nil || !errors.Is(err, query.ErrNotFound) {
		t.Errorf("a hit for a part of a word: %v, %v", err, r)
	}
	// Nothing at all: one line, no answer.
	if r, err = query.Symbol(c, "zzyzxq", query.SymbolOpts{}); r != nil || err == nil || err.Error() != "symbol zzyzxq: not found" {
		t.Errorf("nothing to offer: %v, %v", err, r)
	}
}

func TestStrictRestoresNotFound(t *testing.T) {
	c := linkWorkspace(t)
	c.Strict = true
	for _, addr := range []string{"hubclient", "packages/edge/src/bin.ts#main", "bin.ts#main", "tools.ts:13", "zzyzxq"} {
		r, err := query.Symbol(c, addr, query.SymbolOpts{})
		if !errors.Is(err, query.ErrNotFound) || errors.Is(err, query.ErrNearest) || r == nil || len(r.Sections) != 0 {
			t.Errorf("%s: %v, %v", addr, err, r)
		}
	}
	r, err := query.Find(c, "zzyzxq", query.FindOpts{})
	wants(t, text(t, r, err), "== find zzyzxq (0)")
	if _, err = query.Outline(c, "bin.ts", query.OutlineOpts{}); !errors.Is(err, query.ErrNotFound) || errors.Is(err, query.ErrNearest) {
		t.Errorf("outline: %v", err)
	}
	if _, err = query.Tests(c, "zzyzxq", query.GraphOpts{}); !errors.Is(err, query.ErrNotFound) {
		t.Errorf("tests: %v", err)
	}
	if _, err = query.Deps(c, "packages/egde", query.GraphOpts{Depth: 1}); !errors.Is(err, query.ErrNotFound) || errors.Is(err, query.ErrNearest) {
		t.Errorf("deps: %v", err)
	}
}

// An ambiguous address is answered for the best candidate: production
// code before tests, the most referenced, the shortest path; a file of
// the working directory before all; the others are one line.
func TestAmbiguousAddressAnswersTheBestCandidate(t *testing.T) {
	c := linkWorkspace(t)
	r, err := query.Symbol(c, "register", query.SymbolOpts{NoBody: true})
	got := text(t, r, err)
	wants(t, got, "AgentStore.register  packages/hub/src/store/agents.ts:4-6  ",
		"also: HubClient.register (packages/edge/src/hub-client.ts:4), register (packages/hub/src/server.test.ts:1)\n")
	if strings.Contains(got, "candidates") {
		t.Errorf("no candidates listing:\n%s", got)
	}
	r, err = query.Callers(c, "register", query.GraphOpts{Depth: 1})
	wants(t, text(t, r, err), "== callers of AgentStore.register (1)\nroutes  packages/hub/src/routes.ts:6\n", "also: HubClient.register (")
	// In the directory of the other class, that one is meant.
	c.Cwd = filepath.Join(c.Repo.Path, "packages", "edge", "src")
	r, err = query.Symbol(c, "register", query.SymbolOpts{NoBody: true})
	wants(t, text(t, r, err), "HubClient.register  packages/edge/src/hub-client.ts:4-6  ", "also: AgentStore.register (")
	c.Cwd = filepath.Join(c.Repo.Path, "packages")
	r, err = query.Symbol(c, "register", query.SymbolOpts{NoBody: true})
	wants(t, text(t, r, err), "AgentStore.register  ")
	// Among repos, the one the working directory is in, even at its root.
	both := workspaceOf(t, "py-lib", "java-lib")
	for i, want := range []string{"Store  src/pylib/store.py:13-22  ", "Store  src/main/java/com/fixture/store/Store.java:8-19  "} {
		both.Cwd = both.Workspace.Repos[i].Path
		r, err = query.Symbol(both, "Store", query.SymbolOpts{NoBody: true})
		wants(t, text(t, r, err), want, "also: Store (")
	}
	// Strictly: the candidates and ErrAmbiguous. The whole qualified name
	// wins there, as it did (the test helper is the only plain register).
	c.Strict = true
	r, err = query.Symbol(c, "register", query.SymbolOpts{NoBody: true})
	wants(t, text(t, r, err), "register  packages/hub/src/server.test.ts:1-3  ")
	r, err = query.Symbol(c, "bin.ts", query.SymbolOpts{})
	if !errors.Is(err, query.ErrNotFound) {
		t.Fatalf("bin.ts: %v, %v", err, r)
	}
}

func TestMoreCandidatesThanTheNoteNames(t *testing.T) {
	files := map[string]string{}
	for _, name := range []string{"a", "b", "c", "d", "e", "f"} {
		files[name+".ts"] = "export class " + strings.ToUpper(name) + " {\n  run(): void {}\n}\n"
	}
	c := tempWorkspace(t, files)
	r, err := query.Symbol(c, "run", query.SymbolOpts{NoBody: true})
	wants(t, text(t, r, err), "A.run  a.ts:2  ", "\nalso: B.run (b.ts:2), C.run (c.ts:2), D.run (d.ts:2), +2 more\n")
}

// A name another repo has in the exact case does not win over the repo
// the query runs in: the symbol of that repo equal ignoring case is
// answered, and the others are named. Only without a candidate there
// does the case-exact name win.
func TestNameOfTheWorkingRepoWinsOverAnExactCaseElsewhere(t *testing.T) {
	c := workspaceOf(t, "go-lib", "py-lib", "java-lib", "doc-repo")
	at := func(repo int) {
		c.Repo, c.Cwd = c.Workspace.Repos[repo], c.Workspace.Repos[repo].Path
	}
	at(0)
	r, err := query.Symbol(c, "lookup", query.SymbolOpts{})
	wants(t, text(t, r, err), "Service.Lookup  service/service.go:17-23\n",
		"also: Service.lookup (py-lib:src/pylib/service.py:11), Service.lookup (java-lib:src/main/java/com/fixture/service/Service.java:23)")
	at(1)
	r, err = query.Symbol(c, "lookup", query.SymbolOpts{})
	wants(t, text(t, r, err), "Service.lookup  src/pylib/service.py:11-15\n")
	at(3) // a heading of the working repo is a candidate like any other
	r, err = query.Symbol(c, "lookup", query.SymbolOpts{})
	wants(t, text(t, r, err), "Lookup  docs/notes.rst:7-20\n", "also: Service.lookup (py-lib:src/pylib/service.py:11), ")
	c.Repo, c.Cwd = nil, t.TempDir() // outside every repo the exact case wins
	r, err = query.Symbol(c, "lookup", query.SymbolOpts{})
	wants(t, text(t, r, err), "Service.lookup  src/pylib/service.py:11-15\n")
	c.Strict = true // and --strict resolves the name as written
	at(0)
	if _, err = query.Symbol(c, "lookup", query.SymbolOpts{}); !errors.Is(err, query.ErrAmbiguous) {
		t.Fatalf("strict: %v", err)
	}
	c.Strict = false
	// Candidates that are only equal ignoring case list those of the
	// working repo first, and no full-text hit after them.
	at(1)
	r, err = query.Symbol(c, "memstore.get", query.SymbolOpts{})
	got := nearest(t, r, err)
	wants(t, got, "== no exact match; nearest: (8)\nMemStore.get  py-lib:src/pylib/store.py:37-41  def get(self, key)\n"+
		"MemStore.Get  go-lib:store/store.go:26-32  ")
	if strings.Contains(got, "doc-repo:") {
		t.Fatalf("full-text hits after candidates found by name:\n%s", got)
	}
}

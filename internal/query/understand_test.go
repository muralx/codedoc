package query_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/query"
)

func TestExplainSymbolSnapshot(t *testing.T) {
	c := indexedWorkspace(t)
	r, err := query.Explain(c, "Service.Lookup")
	snapshot(t, "explain_service_lookup", run(t, r, err))
	if len(r.Sections) != 3 || len(r.Sections[0].Entries) != 5 || r.Sections[0].Entries[4].Depth != 4 {
		t.Fatalf("%+v", r.Sections)
	}
}

func TestExplainRepoReadmeAndMissing(t *testing.T) {
	c := indexedWorkspace(t)
	lib := c.Workspace.Repos[0]
	readme := "# go-lib\n\n[![ci](x)](y)\nThe fixture library:\nstores and services.\n\nMore text.\n"
	if err := os.WriteFile(filepath.Join(lib.Path, "README.md"), []byte(readme), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := query.Explain(c, "MemStore.Get")
	mustContain(t, run(t, r, err), "go-lib  go-lib:README.md  repo  The fixture library: stores and services.\n",
		"      MemStore  store/store.go:18-20  type MemStore struct  MemStore is an in-memory Store.\n",
		"== top callers (1)")
	mustContain(t, keyed(t, r), "      "+libStore+"MemStore  store/store.go:18-20  type MemStore struct", "[doc:ok]")
	if _, err := query.Explain(c, "Nope"); !errors.Is(err, query.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestExplainPaths(t *testing.T) {
	c := indexedWorkspace(t)
	for target, want := range map[string]string{
		"go-lib:store":                 "== explain github.com/fixture/lib/store (3)",
		"github.com/fixture/lib/store": "== main symbols (6)",
		"go-lib:store/store.go":        "== explain go-lib:store/store.go (3)",
		"@fixture/domain":              "== explain @fixture/domain",
	} {
		r, err := query.Explain(c, target)
		mustContain(t, run(t, r, err), want)
	}
	c.Repo = c.Workspace.Repos[0]
	r, err := query.Explain(c, "store/store.go")
	mustContain(t, run(t, r, err), "== symbols (4)", "Package store persists the fixture's key-value data.")
	r, err = query.Explain(c, "github.com/fixture/lib/service")
	mustContain(t, run(t, r, err), "Package service exposes the fixture's use cases.")
}

func TestFlowSnapshot(t *testing.T) {
	c := indexedWorkspace(t)
	r, err := query.Flow(c, "go-app:go:github.com/fixture/app:main", query.FlowOpts{Depth: 3})
	snapshot(t, "flow_main", run(t, r, err))
	r, err = query.Flow(c, "go-app:go:github.com/fixture/app:main", query.FlowOpts{Depth: 3, DocumentedOnly: true})
	text := run(t, r, err)
	snapshot(t, "flow_main_documented", text)
	if strings.Contains(text, appKey+"run") || strings.Contains(text, "fallback") {
		t.Fatal(text)
	}
	r, err = query.Flow(c, "go-app:go:github.com/fixture/app:main", query.FlowOpts{Depth: 1})
	if text := run(t, r, err); strings.Contains(text, "Service.Lookup") {
		t.Fatalf("depth 1 expanded run:\n%s", text)
	}
}

func TestFlowAmbiguousAndCycles(t *testing.T) {
	c := ambiguousWorkspace(t)
	r, err := query.Flow(c, "TestRelay", query.FlowOpts{Depth: 2})
	mustContain(t, run(t, r, err), "\n  ? Cache.Get | NullStore.Get | Store.Get | MemStore.Get  store/relay_test.go:6\n")
	mustContain(t, keyed(t, r), "  ?  store/relay_test.go:6  one of ")
	r, err = query.Flow(c, "TestRelay", query.FlowOpts{Depth: 2, DocumentedOnly: true})
	mustContain(t, run(t, r, err), "\n  ? Cache.Get | ")
	lib := c.Workspace.Repos[0]
	appendTo(t, filepath.Join(lib.Path, "service", "service.go"),
		"\n// Ping bounces.\nfunc Ping(n int) { pong(n) }\n\nfunc pong(n int) { Ping(n - 1) }\n")
	c.Fresh = nil // check freshness again
	r, err = query.Flow(c, "Ping", query.FlowOpts{Depth: 5})
	text := run(t, r, err)
	mustContain(t, text, "== flow from Ping (3)\nPing  ", "\n  pong  ", "\n    Ping  ", "[cycle]")
	if text = keyed(t, r); strings.Count(text, "service:Ping ") != 3 { // title, root, cycle
		t.Fatalf("cycle expanded:\n%s", text)
	}
	r, err = query.Flow(c, "Ping", query.FlowOpts{Depth: 5, DocumentedOnly: true})
	mustContain(t, run(t, r, err), "\n  Ping  service/service.go:", "[cycle]")
	if _, err := query.Flow(c, "Ping", query.FlowOpts{}); err == nil {
		t.Fatal("depth 0 accepted")
	}
	c.Strict = true
	if _, err := query.Flow(c, "Get", query.FlowOpts{Depth: 1}); !errors.Is(err, query.ErrAmbiguous) {
		t.Fatal(err)
	}
}

func TestFlowExpandsEachSymbolOnceBreadthFirst(t *testing.T) {
	c := indexedWorkspace(t)
	var b strings.Builder
	b.WriteString("\nfunc Top() { Wide(); Shared() }\n\nfunc Other() { Shared(); Twice() }\n\nfunc Twice() { Shared() }\n")
	b.WriteString("\nfunc Shared() { leaf() }\n\nfunc leaf() {}\n\nfunc Wide() {\n\tShared()\n")
	for i := range 250 {
		fmt.Fprintf(&b, "\tw%d()\n", i)
	}
	b.WriteString("}\n")
	for i := range 250 {
		fmt.Fprintf(&b, "\nfunc w%d() {}\n", i)
	}
	appendTo(t, filepath.Join(c.Workspace.Repos[0].Path, "service", "service.go"), b.String())
	c.Fresh = nil
	r, err := query.Flow(c, "Other", query.FlowOpts{Depth: 3})
	text := run(t, r, err)
	mustContain(t, text, "\n  Shared  ", "\n    leaf  ", "\n    Shared  service/service.go:", "[see above]")
	if strings.Count(text, "  leaf  ") != 1 {
		t.Fatalf("Shared expanded twice:\n%s", text)
	}
	r, err = query.Flow(c, "Top", query.FlowOpts{Depth: 2})
	text = run(t, r, err)
	mustContain(t, text, "\n  Shared  ", "[see below]", "stopped at 200 nodes")
}

// appendTo appends text to the file at path.
func appendTo(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(text)
	if cerr := f.Close(); err != nil || cerr != nil {
		t.Fatal(err, cerr)
	}
}

func TestExplainModuleRanksTestsLast(t *testing.T) {
	c := indexedWorkspace(t)
	r, err := query.Explain(c, "go-lib:store")
	text := run(t, r, err)
	mustContain(t, text, "== main symbols (6)")
	if last := r.Sections[1].Entries[len(r.Sections[1].Entries)-1]; last.Key != libStore+"TestMemStorePutGet" {
		t.Fatalf("a test is not last:\n%s", text)
	}
}

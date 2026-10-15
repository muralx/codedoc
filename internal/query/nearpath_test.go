package query_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/query"
	"github.com/muralx/codedoc/internal/testutil"
)

func TestFindFallsThroughToSearch(t *testing.T) {
	c := linkWorkspace(t)
	// Several words ask where something is done, answered as codedoc where answers.
	r, err := query.Find(c, "message delivered through the channel hook", query.FindOpts{})
	want, werr := query.Where(c, "message delivered through the channel hook", query.WhereOpts{})
	if got := text(t, r, err); got != text(t, want, werr) || !strings.Contains(got, "== where \"message delivered through the channel hook\" (") ||
		!strings.Contains(got, "Delivery  docs/delivery.md:1-3\n1\t# Delivery\n2\t\n3\tA message is delivered through the channel") {
		t.Errorf("several words:\n%s", got)
	}
	// One word no symbol has: the search, saying so.
	r, err = query.Find(c, "nags", query.FindOpts{})
	wants(t, text(t, r, err), "== no symbol named so; search results: (1)\nDelivery  docs/delivery.md:1-3  ",
		"3\tA message is delivered through the channel; the hook nags when it stays unread.\n")
	// A filter search does not have is dropped, one it has is kept.
	r, err = query.Find(c, "nags", query.FindOpts{Kind: "constructor"})
	wants(t, text(t, r, err), "== no symbol named so; search results: (1)")
	r, err = query.Find(c, "nags", query.FindOpts{Lang: "go"})
	wants(t, text(t, r, err), "== no symbol named so; search results: (0)")
	// A name that matches stays a find; a glob that matches nothing is an
	// empty answer, and so is a heading asked for by kind.
	r, err = query.Find(c, "runMain", query.FindOpts{})
	wants(t, text(t, r, err), "== find runMain (1)")
	r, err = query.Find(c, "zz*", query.FindOpts{})
	wants(t, text(t, r, err), "== find zz* (0)")
	r, err = query.Find(c, "no such heading", query.FindOpts{Kind: "section"})
	wants(t, text(t, r, err), "== find no such heading (0)")
	r, err = query.Find(c, "delivery", query.FindOpts{Kind: "section"})
	wants(t, text(t, r, err), "== find delivery (1)")
}

func TestPathArgumentThatNamesNothing(t *testing.T) {
	c := linkWorkspace(t)
	// The only file with that name is answered, in outline and lines.
	r, err := query.Outline(c, "tools.ts", query.OutlineOpts{})
	wants(t, text(t, r, err), "== packages/edge/src/tools.ts, 14 lines (3)", "answered for packages/edge/src/tools.ts\n")
	r, err = query.Lines(c, "src/cli.ts", "7")
	wants(t, text(t, r, err), "packages/edge/src/cli.ts:7  in runMain (7-9)\n7\tfunction runMain(): void {\n",
		"answered for packages/edge/src/cli.ts\n")
	r, err = query.Outline(c, "hub/src/store", query.OutlineOpts{})
	wants(t, text(t, r, err), "== packages/hub/src/store (1)", "answered for packages/hub/src/store\n")
	// Several: listed. One of them in the working directory: that one.
	r, err = query.Outline(c, "bin.ts", query.OutlineOpts{})
	wants(t, nearest(t, r, err), "== no exact match; nearest: (2)\npackages/edge/src/bin.ts  9 lines\npackages/hub/src/bin.ts  2 lines\n")
	c.Cwd = filepath.Join(c.Repo.Path, "packages", "hub")
	r, err = query.Outline(c, "bin.ts", query.OutlineOpts{})
	wants(t, text(t, r, err), "== packages/hub/src/bin.ts, 2 lines (1)", "answered for packages/hub/src/bin.ts")
	c.Cwd = c.Repo.Path
	// A typo away: listed, files and directories.
	r, err = query.Outline(c, "packages/edge/src/tool.ts", query.OutlineOpts{})
	wants(t, nearest(t, r, err), "== no exact match; nearest: (1)\npackages/edge/src/tools.ts  14 lines\n")
	r, err = query.Outline(c, "packages/egde/src", query.OutlineOpts{})
	wants(t, nearest(t, r, err), "\npackages/edge/src/  directory\n")
	r, err = query.Lines(c, "packages/edge/src/tool.ts", "1")
	wants(t, nearest(t, r, err), "packages/edge/src/tools.ts  14 lines")
	// Nothing near: one line.
	for _, target := range []string{"dist/x.js", "packages/nowhere/at/all.ts"} {
		if r, err = query.Outline(c, target, query.OutlineOpts{}); r != nil || err == nil ||
			err.Error() != "outline "+target+": not found: no such file or directory in link" {
			t.Errorf("%s: %v, %v", target, err, r)
		}
	}
	if r, err = query.Lines(c, "nowhere.ts", "1"); r != nil || err == nil || err.Error() != "lines nowhere.ts: not found: no such file in link" {
		t.Errorf("lines: %v, %v", err, r)
	}
}

// A write is never made to a nearest or a best candidate.
func TestWritesStayStrict(t *testing.T) {
	c := linkWorkspace(t)
	file := filepath.Join(c.Repo.Path, "packages/hub/src/store/agents.ts")
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	lib := workspaceOf(t, "go-lib")
	for _, w := range []struct {
		c    *query.Ctx
		addr string
		is   error
		want string
	}{
		{c, "agentstore", query.ErrNotFound, "doc set agentstore: not found; look its name up with codedoc find, or its file with codedoc outline"},
		{c, "packages/hub/src/store/agents.ts#Agent", query.ErrNotFound, "agents.ts#Agent: not found; "},
		{c, "packages/egde", query.ErrNotFound, "doc set packages/egde: not found; "},
		{lib, "Put", query.ErrAmbiguous, "doc set Put: ambiguous (3 candidates); address one by its key: "},
	} {
		var failed *query.DocSetError
		_, err := query.DocSet(w.c, w.addr, "A doc.")
		if !errors.As(err, &failed) || !errors.Is(err, w.is) || errors.Is(err, query.ErrNearest) || !strings.Contains(err.Error(), w.want) {
			t.Errorf("%s: %v", w.addr, err)
		}
	}
	if after, err := os.ReadFile(file); err != nil || string(after) != string(before) {
		t.Errorf("nothing was written: %v\n%s", err, after)
	}
	if _, err = query.DocSet(c, "AgentStore.register", "A doc."); err != nil {
		t.Fatalf("the exact address: %v", err)
	}
	if after, err := os.ReadFile(file); err != nil || strings.Count(string(after), "A doc.") != 1 {
		t.Errorf("the exact address was written: %v\n%s", err, after)
	}
}

func TestBatchWritesStayStrict(t *testing.T) {
	c := linkWorkspace(t)
	before, err := os.ReadFile(filepath.Join(c.Repo.Path, "packages/hub/src/store/agents.ts"))
	if err != nil {
		t.Fatal(err)
	}
	_, failures, err := query.DocSetBatch(c, []query.DocRecord{{Addr: "agentstore", Text: "A doc."}, {Addr: "bin.ts#main", Text: "A doc."}})
	if err != nil || len(failures) != 2 || !strings.HasPrefix(failures[0], "agentstore: not found; ") ||
		!strings.HasPrefix(failures[1], "bin.ts#main: not found; ") {
		t.Errorf("a batch: %v %q", err, failures)
	}
	// The context answers leniently again after a write.
	r, err := query.Symbol(c, "agentstore", query.SymbolOpts{NoBody: true})
	wants(t, text(t, r, err), "answered for AgentStore")
	after, err := os.ReadFile(filepath.Join(c.Repo.Path, "packages/hub/src/store/agents.ts"))
	if err != nil || string(after) != string(before) {
		t.Errorf("nothing was written:\n%s", after)
	}
}

func TestModuleAddressThatNamesNothingOrSeveral(t *testing.T) {
	c := indexedWorkspace(t)
	c.Repo = c.Workspace.Repos[0]
	// The only module ending with the address is answered.
	r, err := query.Deps(c, "lib/service", query.GraphOpts{Depth: 1})
	wants(t, text(t, r, err), "github.com/fixture/lib/store", "answered for github.com/fixture/lib/service")
	// Several near: listed.
	r, err = query.Deps(c, "fixture", query.GraphOpts{Depth: 1})
	wants(t, nearest(t, r, err), "== no exact match; nearest: (", "github.com/fixture/lib/store  go-lib:store  go ")
	if r, err = query.Deps(c, "zzyzxq", query.GraphOpts{Depth: 1}); r != nil || err == nil || err.Error() != "module zzyzxq: not found" {
		t.Errorf("no module near: %v, %v", err, r)
	}
	// tests tries the address as a module before the nearest symbols.
	r, err = query.Tests(c, "github.com/fixture/lib/store", query.GraphOpts{})
	wants(t, text(t, r, err), "== tests of module github.com/fixture/lib/store (")
	r, err = query.Tests(c, "memstore", query.GraphOpts{})
	wants(t, text(t, r, err), "== tests of MemStore (", "answered for MemStore")
}

// A module identity two repos provide is answered for the module of the
// current repo, the other named; strictly it is ambiguous.
func TestModuleIdentityOfTwoRepos(t *testing.T) {
	one := &config.Repo{Name: "one", Path: testutil.CopyFixture(t, "go-lib")}
	two := &config.Repo{Name: "two", Path: testutil.CopyFixture(t, "go-lib")}
	c := indexRepos(t, one, two)
	c.Repo = two
	r, err := query.Deps(c, "github.com/fixture/lib/service", query.GraphOpts{Depth: 1})
	wants(t, text(t, r, err), "github.com/fixture/lib/store  two:store", "also: github.com/fixture/lib/service (one:service)")
	c.Repo = nil
	r, err = query.Deps(c, "github.com/fixture/lib/service", query.GraphOpts{Depth: 1})
	wants(t, text(t, r, err), "also: github.com/fixture/lib/service (two:service)")
	c.Strict = true
	r, err = query.Deps(c, "github.com/fixture/lib/service", query.GraphOpts{Depth: 1})
	if !errors.Is(err, query.ErrAmbiguous) || !strings.Contains(render(t, r), "== candidates (2)") {
		t.Errorf("strictly: %v\n%v", err, r)
	}
}

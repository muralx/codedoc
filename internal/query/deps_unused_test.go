package query_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/query"
)

// again returns a new context over the store and deps.db of c with
// --deps set: the next query runs the freshness check again.
func again(t *testing.T, c *query.Ctx) *query.Ctx {
	t.Helper()
	next := &query.Ctx{Store: c.Store, Workspace: c.Workspace, DepsDB: c.DepsDB, Deps: true}
	t.Cleanup(func() { _ = next.Close() })
	return next
}

// plainAnswers runs the queries that may land in plain-js and returns
// what they print, errors as text.
func plainAnswers(t *testing.T, c *query.Ctx) string {
	t.Helper()
	var b strings.Builder
	add := func(r *output.Response, err error) {
		if err != nil {
			b.WriteString("error: " + err.Error() + "\n")
			return
		}
		b.WriteString(render(t, r))
	}
	add(query.Find(c, "shout", query.FindOpts{}))
	add(query.Search(c, "upper case", query.SearchOpts{}))
	add(query.Outline(c, "plain-js", query.OutlineOpts{}))
	add(query.Callers(c, "shout", query.GraphOpts{Depth: 1}))
	return b.String()
}

// A package answers find, search, outline and callers while a source
// file imports it. Once the last import is gone it is unused: its rows
// are still in deps.db, where another workspace may need them, and no
// answer of this workspace shows them.
func TestAnUnusedPackageLeavesEveryAnswer(t *testing.T) {
	c := depsWorkspace(t, "deps-app")
	root := c.Workspace.Repos[0].Path
	file := filepath.Join(root, "src", "plain.ts")
	src := "import { shout } from \"plain-js\";\n\n/** Says a label loudly. */\nexport function loud(): string {\n  return shout(\"x\");\n}\n"
	if out := plainAnswers(t, again(t, c)); strings.Contains(out, "plain-js@") {
		t.Fatalf("a package no file imports is in no answer:\n%s", out)
	}
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out := plainAnswers(t, again(t, c))
	wants(t, out, "\nshout  index.d.ts:4  ", "[dep plain-js@2.0.1]",
		"== npm/plain-js@2.0.1 modules", "== callers of shout [dep plain-js@2.0.1] (1)",
		"\nloud  src/plain.ts:5  return shout(\"x\");\n")
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	last := again(t, c)
	if out := plainAnswers(t, last); strings.Contains(out, "plain-js@") {
		t.Errorf("an unused package still answers:\n%s", out)
	}
	if last.DepsPending != 0 {
		t.Errorf("an unused package does not wait: %d pending", last.DepsPending)
	}
	r, err := query.Overview(last)
	if out := text(t, r, err); strings.Contains(out, "plain-js") || strings.Contains(out, "lint-tool") {
		t.Errorf("the overview lists no unused package:\n%s", out)
	}
}

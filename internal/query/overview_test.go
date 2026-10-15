package query_test

import (
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/query"
)

func TestOverviewSnapshot(t *testing.T) {
	c := workspaceOf(t, "go-lib", "go-app", "ts-domain", "py-lib", "java-lib", "cfg-repo")
	r, err := query.Overview(c)
	text := run(t, r, err)
	snapshot(t, "overview", text)
	mustContain(t, text, "== repos (6)", "== entry points", "[main]", "== recent commits (6)", "fixture: initial",
		"== doc coverage (6)", "== modules", "github.com/fixture/lib/store  go-lib:store", "Package store persists")
}

func TestOverviewEntryPointsAndBudget(t *testing.T) {
	c := tempWorkspace(t, map[string]string{
		"go.mod":            "module x/m\n",
		"cmd/tool/main.go":  "package main\n\nimport \"net/http\"\n\nfunc main() { serve() }\n\nfunc serve() { http.ListenAndServe(\":80\", nil) }\n",
		"bin/run.sh":        "echo hi\n",
		"web/app.py":        "import uvicorn\n\n\ndef start():\n    uvicorn.run(None)\n",
		"README.md":         "# Tool\n\n## Usage\n\n```\n# not a heading\n```\n\n### Deep\n",
		"CLAUDE.md":         "# Rules\n",
		"lib/internal/x.go": "package internal\n",
	})
	r, err := query.Overview(c)
	text := run(t, r, err)
	mustContain(t, text, "cmd/tool/main.go\n  main  :5  func main()  [main]\n",
		"  serve  :7  func serve()  [calls http.ListenAndServe]\n",
		"\nstart  web/app.py:4-5  def start()  [calls uvicorn.run]\n",
		"r:cmd/tool/  command directory, 1 file", "r:bin/run.sh  command directory, 1 file",
		"r:cmd/tool/main.go  entry file", "r:web/app.py  entry file",
		"r:README.md  # Tool", "r:README.md  ## Usage", "r:CLAUDE.md  # Rules")
	if strings.Contains(text, "not a heading") || strings.Contains(text, "### Deep") {
		t.Fatalf("headings:\n%s", text)
	}
}

// TestOverviewHeaderCountsEveryRepo checks the header counts the repos
// of the workspace, the ones without a module, an entry point or any
// other entry keyed by repo (a repo of documents, a config repo)
// included, and names the current repo, else the first.
func TestOverviewHeaderCountsEveryRepo(t *testing.T) {
	c := workspaceOf(t, "go-lib", "go-app", "ts-domain", "cfg-repo", "doc-repo")
	r, err := query.Overview(c)
	text := run(t, r, err)
	mustContain(t, text, "5 repos (go-lib@", " +4)\n== repos (5)\n", "\ndoc-repo  main@")
	c.Repo = c.Workspace.Repos[4]
	r, err = query.Overview(c)
	mustContain(t, run(t, r, err), "5 repos (doc-repo@")
	c.Filter = true
	r, err = query.Overview(c)
	if text := run(t, r, err); !strings.HasPrefix(text, "doc-repo@") {
		t.Fatalf("--repo keeps the header of that repo:\n%s", text)
	}
}

package query_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/query"
)

func TestDocCheckCountsAndProblems(t *testing.T) {
	c := tempWorkspace(t, map[string]string{
		"go.mod":          "module x/m\n",
		"a.go":            "package m\n\n// Returns one.\nfunc One() int { return 1 }\n\n// A Two is two.\nfunc Two() int { return 2 }\n\nfunc Three() {}\n",
		"p/A.java":        "package p;\n\n/** Detached. */\n\nclass A {\n  /** Attached. */\n  void f() {}\n\n  /** Lost. */\n\n  int x;\n}\n",
		"pkg/__init__.py": "'''Single quoted module.'''\n",
		"pkg/m.py":        "def f():\n    '''Single.'''\n    return 1\n\n\ndef g():\n    \"\"\"Double.\"\"\"\n    return 1\n",
	})
	r, err := query.DocCheck(c)
	text := run(t, r, err)
	mustContain(t, text, "== doc states (1)", "r  symbols ok:7 stale:0 missing:1  modules ok:1 missing:3",
		"== doc problems (5)", "p/A.java\n  A.x  :11  javadoc detached",
		"\nOne  a.go:4  go doc does not start with One  [warning]",
		"  A  :5-12  javadoc detached from the declaration by a blank line  [problem]",
		"\nf  pkg/m.py:1-3  docstring uses single quotes; use triple double quotes  [problem]",
		"pkg  r:pkg/__init__.py:1  module docstring uses single quotes")
}

func TestDocCheckSyncsPastTheFreshnessLimit(t *testing.T) {
	c := indexedWorkspace(t)
	t.Setenv("CODEDOC_FRESH_MAX_FILES", "0")
	writeAppend(t, filepath.Join(c.Workspace.Repos[0].Path, "store", "null.go"), "\nfunc Later() {}\n")
	r, err := query.DocCheck(c)
	text := run(t, r, err)
	mustContain(t, text, ") dirty\n")
	if strings.Contains(text, "fresh:no") || r.Header.Dirty != 1 {
		t.Errorf("header %+v:\n%s", r.Header, text)
	}
	out, err := query.Outline(c, "go-lib:store/null.go", query.OutlineOpts{})
	mustContain(t, run(t, out, err), "Later")
}

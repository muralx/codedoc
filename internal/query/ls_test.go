package query_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/query"
	"github.com/muralx/codedoc/internal/testutil"
)

// lsWorkspace indexes doc-repo after a second commit and with one
// modified and one untracked file, every file at a known time:
//
//	docs/design.md           mtime 03-01 10:00, committed 02-01 (second commit)
//	docs/guide.adoc          mtime 03-02 12:30, modified
//	docs/new.md              mtime 03-03 09:15, untracked
//	everything else          mtime and commit 2026-01-01 00:00
//
// docs/runbooks is excluded by the workspace, docs/debug.log and tmp/
// ignored by git, docs/node_modules excluded by the built-in rules and
// docs/blob.bin binary.
func lsWorkspace(t *testing.T) *query.Ctx {
	t.Helper()
	root := testutil.CopyFixture(t, "doc-repo")
	editRepo(t, root, "docs/design.md", "Glossary of terms.", "Glossary of the terms used.")
	gitIn(t, root, "2026-02-01T08:00:00Z", "commit", "-qam", "design: glossary")
	editRepo(t, root, "docs/guide.adoc", "Fetch the module.", "Fetch the module first.")
	for rel, body := range map[string]string{"docs/new.md": "# Brand new\n\nText.\n", ".gitignore": "*.log\ntmp/\n",
		"docs/debug.log": "x\n", "tmp/a/b.txt": "x\n", "docs/node_modules/m/readme.md": "# m\n", "docs/blob.bin": "a\x00b"} {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for rel, at := range map[string]string{"docs/design.md": "2026-03-01T10:00:00Z", "docs/guide.adoc": "2026-03-02T12:30:00Z",
		"docs/new.md": "2026-03-03T09:15:00Z", ".gitignore": "2026-01-01T00:00:00Z"} {
		when, err := time.Parse(time.RFC3339, at)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(filepath.Join(root, rel), when, when); err != nil {
			t.Fatal(err)
		}
	}
	c := indexRepos(t, &config.Repo{Name: "doc-repo", Path: root})
	c.Workspace.Exclude = []string{"**/runbooks/**"}
	c.Now = func() time.Time { return time.Date(2026, 3, 3, 12, 0, 0, 0, time.UTC) }
	return c
}

// locs returns the locations of a section's entries, joined by spaces.
func locs(sec output.Section) string {
	out := make([]string, len(sec.Entries))
	for i, e := range sec.Entries {
		out[i] = e.Loc
	}
	return strings.Join(out, " ")
}

func ls(t *testing.T, c *query.Ctx, o query.LsOpts, args ...string) *output.Response {
	t.Helper()
	resp, err := query.Ls(c, args, o)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestLsListsNewestFirstWithMetadata(t *testing.T) {
	c := lsWorkspace(t)
	snapshot(t, "ls_docs", render(t, ls(t, c, query.LsOpts{}, "doc-repo:docs")))
}

func TestLsSorts(t *testing.T) {
	c := lsWorkspace(t)
	for sort, want := range map[string]string{
		"name":      "docs/CHANGES.txt docs/design.md docs/guide.adoc docs/new.md docs/notes.rst",
		"size":      "docs/design.md docs/notes.rst docs/guide.adoc docs/CHANGES.txt docs/new.md",
		"lines":     "docs/design.md docs/notes.rst docs/guide.adoc docs/CHANGES.txt docs/new.md",
		"committed": "docs/new.md docs/guide.adoc docs/design.md docs/CHANGES.txt docs/notes.rst",
	} {
		if got := locs(ls(t, c, query.LsOpts{Sort: sort}, "doc-repo:docs").Sections[0]); got != want {
			t.Errorf("--sort %s:\n got %s\nwant %s", sort, got, want)
		}
	}
	got := locs(ls(t, c, query.LsOpts{Reverse: true}, "doc-repo:docs").Sections[0])
	if !strings.HasSuffix(got, "docs/design.md docs/guide.adoc docs/new.md") {
		t.Errorf("--reverse: %s", got)
	}
	if _, err := query.Ls(c, nil, query.LsOpts{Sort: "age"}); err == nil || !strings.Contains(err.Error(), "valid: mtime, committed") {
		t.Errorf("--sort age: %v", err)
	}
}

func TestLsFilters(t *testing.T) {
	c := lsWorkspace(t)
	for name, tc := range map[string]struct {
		o    query.LsOpts
		args []string
		want string
	}{
		"since duration": {query.LsOpts{Since: "2d"}, nil, "docs/new.md docs/guide.adoc"},
		"since date":     {query.LsOpts{Since: "2026-03-01"}, nil, "docs/new.md docs/guide.adoc docs/design.md"},
		"until date":     {query.LsOpts{Until: "2026-03-01 23:00", Since: "2026-02-01"}, nil, "docs/design.md"},
		"since ref":      {query.LsOpts{Since: "HEAD"}, nil, "docs/new.md docs/guide.adoc docs/design.md"},
		"kind doc":       {query.LsOpts{Kinds: []string{"config", "other"}}, nil, ".gitignore settings.json"},
		"lang family":    {query.LsOpts{Lang: "doc", Sort: "name"}, []string{"doc-repo:docs/notes.rst", "doc-repo:settings.json"}, "docs/notes.rst"},
		"lang precise":   {query.LsOpts{Lang: "rst"}, nil, "docs/notes.rst"},
		"depth 1":        {query.LsOpts{Depth: 1}, nil, ".gitignore settings.json"},
		"depth 2 docs":   {query.LsOpts{Depth: 1, Sort: "name"}, []string{"doc-repo:docs"}, "docs/CHANGES.txt docs/design.md docs/guide.adoc docs/new.md docs/notes.rst"},
		"glob":           {query.LsOpts{Sort: "name"}, []string{"doc-repo:**/*.md"}, "docs/design.md docs/new.md"},
		"one file":       {query.LsOpts{}, []string{"doc-repo:docs/notes.rst"}, "docs/notes.rst"},
	} {
		if got := locs(ls(t, c, tc.o, tc.args...).Sections[0]); got != tc.want {
			t.Errorf("%s:\n got %s\nwant %s", name, got, tc.want)
		}
	}
	for name, o := range map[string]query.LsOpts{"kind": {Kinds: []string{"docs"}}, "depth": {Depth: -1},
		"since": {Since: "yesterday"}, "until": {Until: "no-such-ref"}} {
		if _, err := query.Ls(c, nil, o); err == nil {
			t.Errorf("bad --%s accepted", name)
		}
	}
}

func TestLsDirs(t *testing.T) {
	c := lsWorkspace(t)
	text := render(t, ls(t, c, query.LsOpts{Dirs: true}))
	mustContain(t, text, "== ls (2)", "./  7 files  2026-03-03 09:15  129 lines  1.8K\n",
		"docs/  5 files  2026-03-03 09:15  123 lines  1.8K\n")
	if got := locs(ls(t, c, query.LsOpts{Dirs: true, Sort: "name", Reverse: true}).Sections[0]); got != "docs/ ./" {
		t.Errorf("--dirs --sort name --reverse: %s", got)
	}
	c = workspaceOf(t, "go-lib", "java-lib")
	text = render(t, ls(t, c, query.LsOpts{Dirs: true, Depth: 1, Sort: "lines"}, "java-lib:src", "go-lib:."))
	mustContain(t, text, "== ls java-lib:src go-lib:. (6)", "java-lib:src/  5 files  2026-01-01 00:00  115 lines  ",
		"java-lib:src/main/  4 files  ", "java-lib:src/test/  1 file  ", "go-lib:./  8 files  ", "go-lib:store/  4 files  ")
	if strings.Contains(text, "src/main/java") {
		t.Errorf("--depth 1 lists one level of directories:\n%s", text)
	}
}

func TestLsNotesHiddenFilesByReason(t *testing.T) {
	c := lsWorkspace(t)
	text := render(t, ls(t, c, query.LsOpts{}))
	mustContain(t, text, "hidden under doc-repo:.: 2 ignored by git (1 whole directory), 1 built-in exclusion, "+
		"1 workspace exclude, 1 not indexable (binary, over 2 MB or language not selected)")
	text = render(t, ls(t, c, query.LsOpts{}, "doc-repo:docs"))
	mustContain(t, text, "hidden under doc-repo:docs: 1 ignored by git, 1 built-in exclusion, 1 workspace exclude, 1 not indexable")
	if strings.Contains(text, "retries.md") || strings.Contains(text, "debug.log") {
		t.Errorf("hidden files are never listed:\n%s", text)
	}
	if text := render(t, ls(t, c, query.LsOpts{}, "doc-repo:docs/notes.rst")); strings.Contains(text, "hidden") {
		t.Errorf("a single file hides nothing:\n%s", text)
	}
}

func TestLsDefaultsToTheWorkingDirectory(t *testing.T) {
	c := lsWorkspace(t)
	c.Repo = c.Workspace.Repos[0]
	c.Cwd = filepath.Join(c.Repo.Path, "docs")
	resp := ls(t, c, query.LsOpts{Since: "2026-03-02"})
	if got := locs(resp.Sections[0]); got != "docs/new.md docs/guide.adoc" {
		t.Errorf("ls in docs: %s", got)
	}
	if got := locs(ls(t, c, query.LsOpts{Sort: "name"}, "*.md").Sections[0]); got != "docs/design.md docs/new.md" {
		t.Errorf("a glob is relative to the working directory: %s", got)
	}
	c.Cwd = c.Repo.Path
	if got := locs(ls(t, c, query.LsOpts{Sort: "name"}, "docs/*.md").Sections[0]); got != "docs/design.md docs/new.md" {
		t.Errorf("glob from the root: %s", got)
	}
	c.Filter = true
	if got := len(ls(t, c, query.LsOpts{}).Sections[0].Entries); got != 7 {
		t.Errorf("--repo lists the repo: %d files", got)
	}
}

func TestLsAcrossReposAndErrors(t *testing.T) {
	c := workspaceOf(t, "doc-repo", "go-lib")
	text := render(t, ls(t, c, query.LsOpts{Sort: "name", Kinds: []string{"test", "config"}}))
	mustContain(t, text, "doc-repo@", " go-lib@", "\ndoc-repo:settings.json  2026-01-01 00:00  4 lines\n",
		"\ngo-lib:store/store_test.go  2026-01-01 00:00  13 lines\n")
	text = render(t, ls(t, c, query.LsOpts{Sort: "name", Kinds: []string{"test", "config"}, Long: true}))
	mustContain(t, text, "doc-repo:settings.json  2026-01-01 00:00  50B  4 lines  config/json",
		"go-lib:store/store_test.go  2026-01-01 00:00  ", "test/go  1 symbol")
	// The files of a directory are listed under it: path, date, lines.
	text = render(t, ls(t, c, query.LsOpts{Sort: "name"}, "go-lib:store"))
	mustContain(t, text, "== ls go-lib:store (4)\nstore/\n  doc.go  2026-01-01 00:00  2 lines\n", "  store.go  2026-01-01 00:00  38 lines\n")
	text = render(t, ls(t, c, query.LsOpts{Sort: "name", Long: true}, "go-lib:store"))
	mustContain(t, text, "  doc.go  2026-01-01 00:00  70B  2 lines  code/go  Package store persists the fixture's key-value data.",
		"  store.go  2026-01-01 00:00  ", "38 lines  code/go  9 symbols")
	resp, err := query.Ls(c, []string{"go-lib:nope"}, query.LsOpts{})
	if !errors.Is(err, query.ErrNotFound) || resp != nil ||
		err.Error() != "ls nope: not found: no indexed file under it; codedoc ls --repo go-lib lists the indexed files" {
		t.Errorf("unknown path: %v, %+v", err, resp)
	}
	for _, arg := range []string{"nope", "*.md", "zz:*.md"} {
		if _, err := query.Ls(c, []string{arg}, query.LsOpts{}); err == nil {
			t.Errorf("ls %s outside a repo succeeded", arg)
		}
	}
}

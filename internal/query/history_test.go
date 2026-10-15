package query_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/query"
)

// gitIn runs git in dir with fixed identity and dates.
func gitIn(t *testing.T, dir, date string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.email=t@x", "-c", "user.name=Tess"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func editRepo(t *testing.T, root, rel, old, repl string) {
	t.Helper()
	p := filepath.Join(root, rel)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), old) {
		t.Fatalf("%s has no %q", rel, old)
	}
	if err := os.WriteFile(p, []byte(strings.Replace(string(b), old, repl, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// historyWorkspace indexes go-lib after three more commits and an
// uncommitted edit; it returns the context and the fixture commit.
func historyWorkspace(t *testing.T) (*query.Ctx, string) {
	t.Helper()
	c := workspaceOf(t, "go-lib")
	root := c.Workspace.Repos[0].Path
	first := gitIn(t, root, "2026-01-01T00:00:00Z", "rev-parse", "HEAD")
	editRepo(t, root, "store/store.go", "return v, nil", "return v + \"\", nil")
	gitIn(t, root, "2026-02-01T00:00:00Z", "commit", "-qam", "get: copy the value")
	editRepo(t, root, "store/store.go", "// Put implements Store.", "// Put stores value under key.")
	editRepo(t, root, "store/store.go", "// NewMemStore creates an empty MemStore.\nfunc NewMemStore() *MemStore { return &MemStore{data: map[string]string{}} }\n", "")
	gitIn(t, root, "2026-03-01T00:00:00Z", "commit", "-qam", "put: better doc")
	if err := os.WriteFile(filepath.Join(root, "store", "extra.go"), []byte("package store\n\n// Extra is new.\nfunc Extra() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	editRepo(t, root, "store/null.go", "package store", "package store\n\n// Unused is uncommitted.\nconst Unused = 1")
	return c, first
}

func TestChanges(t *testing.T) {
	c, first := historyWorkspace(t)
	r, err := query.Changes(c, first)
	text := run(t, r, err)
	mustContain(t, text, "== changed files since "+first+" (3)",
		"go-lib:store/extra.go  added, 1 symbol change", "go-lib:store/null.go  modified, 1 symbol change",
		"go-lib:store/store.go  modified, 3 symbol changes",
		"store/store.go\n  MemStore.Get  :24-30  ", "[body-changed] [doc:stale]",
		"  MemStore.Put  :33-36  ", "[doc-changed] [doc:ok]",
		"\nExtra  store/extra.go:4  func Extra()  Extra is new.  [added] [doc:ok]",
		"\nUnused  store/null.go:4", "[added] [doc:ok]",
		"go-lib:store/store.go:23@"+first[:7]+"  func NewMemStore() *MemStore  [removed]")
	r, err = query.Changes(c, "HEAD")
	text = run(t, r, err)
	if strings.Contains(text, "store/store.go") || !strings.Contains(text, "store/extra.go") {
		t.Fatalf("--since HEAD lists only uncommitted changes:\n%s", text)
	}
	if _, err := query.Changes(c, "no-such-ref"); err == nil {
		t.Fatal("unknown ref accepted")
	}
}

func TestHistoryAndAt(t *testing.T) {
	c, first := historyWorkspace(t)
	r, err := query.History(c, libStore+"MemStore.Get", 0)
	text := run(t, r, err)
	mustContain(t, text, "== history of MemStore.Get (2)", "2026-02-01 Tess  get: copy the value",
		"2026-01-01 fixture  fixture: initial")
	mustContain(t, keyed(t, r), "== history of "+libStore+"MemStore.Get (2)")
	r, err = query.History(c, libStore+"Extra", 0)
	mustContain(t, run(t, r, err), "store/extra.go has no committed history")
	r, err = query.At(c, libStore+"MemStore.Get", query.AtOpts{Ref: first})
	text = run(t, r, err)
	mustContain(t, text, "store/store.go:25@"+first, "25\t// Get implements Store.", "31\t\treturn v, nil")
	r, err = query.At(c, libStore+"MemStore.Get", query.AtOpts{Ref: first, Diff: query.WorkTree})
	text = run(t, r, err)
	mustContain(t, text, "--- "+first+":store/store.go\n+++ worktree:store/store.go\n", "-\treturn v, nil\n+\treturn v + \"\", nil\n")
	r, err = query.At(c, libStore+"MemStore.Get", query.AtOpts{Ref: "HEAD", Diff: query.WorkTree})
	mustContain(t, run(t, r, err), "no changes between HEAD and worktree")
	// A symbol that is not at the ref is one line: no response carries
	// a header before it.
	r, err = query.At(c, libStore+"Extra", query.AtOpts{Ref: first})
	if want := "symbol Extra at " + first + ": not found: the file or the symbol may have moved; codedoc history " +
		libStore + "Extra lists its commits"; !errors.Is(err, query.ErrNotFound) || r != nil || err.Error() != want {
		t.Fatalf("symbol absent at the ref: %v, %+v", err, r)
	}
}

func TestHistoryQueriesOutsideGit(t *testing.T) {
	c := tempWorkspace(t, map[string]string{"go.mod": "module x/m\n", "a.go": "package m\n\n// F is f.\nfunc F() {}\n"})
	r, err := query.Changes(c, "HEAD")
	mustContain(t, run(t, r, err), "r is not a git repository")
	r, err = query.History(c, "F", 0)
	mustContain(t, run(t, r, err), "a.go has no committed history")
	r, err = query.DocCheck(c)
	mustContain(t, run(t, r, err), "r  symbols ok:1 stale:0 missing:0")
	if _, err := query.At(c, "F", query.AtOpts{Ref: "HEAD"}); err == nil {
		t.Fatal("at outside git")
	}
	if _, err := query.History(c, "Nope", 0); !errors.Is(err, query.ErrNotFound) {
		t.Fatalf("unknown symbol: %v", err)
	}
	if _, err := query.At(c, "Nope", query.AtOpts{Ref: "HEAD"}); !errors.Is(err, query.ErrNotFound) {
		t.Fatalf("unknown symbol: %v", err)
	}
	if _, err := query.DocSet(c, "Nope", "x"); !errors.Is(err, query.ErrNotFound) {
		t.Fatalf("unknown symbol: %v", err)
	}
	if _, err := query.DocSet(c, "r:nowhere/", "x"); !errors.Is(err, query.ErrNotFound) {
		t.Fatalf("unknown module: %v", err)
	}
}

// TestChangesSkipsReposWithoutTheRef tags one repo of two: changes
// --since the tag lists that repo and names the other as skipped; a ref
// no repo has is still an error.
func TestChangesSkipsReposWithoutTheRef(t *testing.T) {
	c := workspaceOf(t, "go-lib", "go-app")
	lib := c.Workspace.Repos[0].Path
	gitIn(t, lib, "2026-01-01T00:00:00Z", "tag", "lib-v1")
	if err := os.WriteFile(filepath.Join(lib, "store", "extra.go"), []byte("package store\n\n// Extra is new.\nfunc Extra() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := query.Changes(c, "lib-v1")
	mustContain(t, run(t, r, err), "go-lib:store/extra.go  added", `"lib-v1" not found in go-app; skipped`)
	if _, err := query.Changes(c, "no-such-ref"); err == nil || !strings.Contains(err.Error(), `"no-such-ref"`) {
		t.Fatalf("a ref no repo has: %v", err)
	}
}

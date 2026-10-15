package query_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/query"
)

// secretFile writes a file outside every repo of the workspace and
// returns its directory and path.
func secretFile(t *testing.T) (dir, file string) {
	t.Helper()
	dir = t.TempDir()
	file = filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(file, []byte("top secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, file
}

// escapingPaths returns path arguments that lead from the repo go-lib
// to the secret file: by climbing out, and through symbolic links
// (to the file and to its directory) planted inside the repo.
func escapingPaths(t *testing.T, c *query.Ctx) []string {
	t.Helper()
	dir, file := secretFile(t)
	root := c.Workspace.Repos[1].Path
	rel, err := filepath.Rel(root, file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(file, filepath.Join(root, "store", "link.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dir, filepath.Join(root, "linkdir")); err != nil {
		t.Fatal(err)
	}
	return []string{"go-lib:" + filepath.ToSlash(rel), "go-lib:store/../../" + filepath.Base(dir) + "/secret.txt",
		"go-lib:store/link.txt", "go-lib:linkdir/secret.txt", "go-lib:linkdir"}
}

func TestPathCommandsRefuseWhatLiesOutsideTheWorkspace(t *testing.T) {
	c := docWorkspace(t)
	for _, p := range escapingPaths(t, c) {
		calls := map[string]func() (result, string){
			"lines":   func() (result, string) { return must(query.Lines(c, p, "1")), "" },
			"outline": func() (result, string) { return must(query.Outline(c, p, query.OutlineOpts{})), "" },
			"ls":      func() (result, string) { return must(query.Ls(c, []string{p}, query.LsOpts{})), "" },
			"explain": func() (result, string) { return must(query.Explain(c, p)), "" },
			"symbol":  func() (result, string) { return must(query.Symbol(c, p+":1", query.SymbolOpts{})), "" },
			"symbol --strict": func() (result, string) {
				strict := *c
				strict.Strict = true
				return must(query.Symbol(&strict, p+":1", query.SymbolOpts{})), "not found"
			},
		}
		for name, call := range calls {
			res, want := call()
			text := ""
			if res.resp != nil {
				text = render(t, res.resp)
			}
			if strings.Contains(text, "top secret") || res.err == nil {
				t.Errorf("%s %s: err %v\n%s", name, p, res.err, text)
				continue
			}
			if want == "" && !errors.Is(res.err, query.ErrOutside) {
				t.Errorf("%s %s: err = %v, want ErrOutside", name, p, res.err)
			}
			if want != "" && !strings.Contains(res.err.Error(), want) {
				t.Errorf("%s %s: err = %v, want %s", name, p, res.err, want)
			}
		}
	}
}

func TestSymbolAtALineOutsideTheWorkspaceSaysSo(t *testing.T) {
	c := docWorkspace(t)
	p := escapingPaths(t, c)[0]
	if resp, err := query.Symbol(c, p+":1", query.SymbolOpts{}); !errors.Is(err, query.ErrOutside) || resp != nil {
		t.Fatalf("err %v, response %v", err, resp)
	}
	c.Strict = true
	resp, err := query.Symbol(c, p+":1", query.SymbolOpts{})
	if !errors.Is(err, query.ErrNotFound) || !strings.Contains(resp.Note, "path is outside the repos of the workspace") {
		t.Fatalf("err %v, note %q", err, resp.Note)
	}
}

func TestFeedbackHashesNoFileOutsideTheWorkspace(t *testing.T) {
	c := docWorkspace(t)
	for _, p := range escapingPaths(t, c) {
		if e := query.FeedbackEntry(c, "t", query.FeedbackOpts{Path: p}); e.CurrentHash != "" || e.IndexedHash != "" {
			t.Errorf("%s: hashed %+v", p, e)
		}
	}
}

func TestPathsRelativeToTheWorkingDirectoryAreContainedToo(t *testing.T) {
	c := docWorkspace(t)
	_, file := secretFile(t)
	c.Repo, c.Cwd = c.Workspace.Repos[1], filepath.Join(c.Workspace.Repos[1].Path, "store")
	rel, err := filepath.Rel(c.Cwd, file)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{rel, file, "../../../../../../../../../../etc/hostname"} {
		if _, err := query.Lines(c, p, "1"); !errors.Is(err, query.ErrOutside) {
			t.Errorf("lines %s: err = %v", p, err)
		}
	}
	// Inside the workspace every form still resolves: a sibling
	// directory, another repo, an absolute path.
	other := filepath.Join(c.Workspace.Repos[0].Path, "docs", "design.md")
	for _, p := range []string{"store.go", "../service/service.go", other} {
		if text := out(t, must(query.Lines(c, p, "1"))); !strings.Contains(text, ":1  ") {
			t.Errorf("lines %s:\n%s", p, text)
		}
	}
}

func TestSymbolicLinksInsideTheWorkspaceStayReadable(t *testing.T) {
	c := docWorkspace(t)
	root := c.Workspace.Repos[1].Path
	if err := os.Symlink(filepath.Join(root, "store", "store.go"), filepath.Join(root, "alias.go")); err != nil {
		t.Fatal(err)
	}
	if text := out(t, must(query.Lines(c, "go-lib:alias.go", "1"))); !strings.Contains(text, "1\t// Package store") {
		t.Errorf("a link to a file of the repo:\n%s", text)
	}
}

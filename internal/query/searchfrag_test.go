package query_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/embed"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/query"
	"github.com/muralx/codedoc/internal/testutil"
)

// launcherSource is a function whose doc says what it does and whose
// body does it some lines further down, like a process launcher in a
// real repository.
const launcherSource = `package store

import "os/exec"

// launchWorker starts the worker binary apart from this process, so it
// outlives the caller.
func launchWorker(bin string, dir string) error {
	cmd := exec.Command(bin)
	cmd.Dir = dir
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Env = append(cmd.Env, "MODE=worker")
	// Detach: the worker gets a process group of its own.
	setDetached(cmd)
	return cmd.Start()
}

func setDetached(cmd *exec.Cmd) { _ = cmd }
`

// launcherWorkspace indexes go-lib with launcherSource as store/launch.go.
func launcherWorkspace(t *testing.T) *query.Ctx {
	t.Helper()
	root := testutil.CopyFixture(t, "go-lib")
	if err := os.WriteFile(filepath.Join(root, "store/launch.go"), []byte(launcherSource), 0o644); err != nil {
		t.Fatal(err)
	}
	c := indexRepos(t, &config.Repo{Name: "go-lib", Path: root})
	c.Embedder = embed.NewFake(4096)
	return c
}

// entryOf returns the entry of the answer named name.
func entryOf(t *testing.T, r *output.Response, name string) output.Entry {
	t.Helper()
	for _, e := range r.Sections[0].Entries {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("no entry %s in\n%s", name, render(t, r))
	return output.Entry{}
}

// The acceptance case of the efficiency pass: a behaviour query finds
// the function and shows the lines of its body that do it, under the
// entry line, with two lines around and without a second call.
func TestSearchShowsTheCodeLinesThatMatch(t *testing.T) {
	c := launcherWorkspace(t)
	r := search(t, c, "start the worker detached", query.SearchOpts{})
	if got := r.Sections[0].Entries[0].Name; got != "launchWorker" {
		t.Fatalf("first hit %s\n%s", got, render(t, r))
	}
	e := entryOf(t, r, "launchWorker")
	// The doc comment (5-6) with two lines after it, and the lines of the
	// body holding the terms (12-15: worker, detach, start) with two
	// around, cut at the end of the function.
	if len(e.Fragments) != 2 || e.Fragments[0].Start != 5 || e.Fragments[0].End != 8 ||
		e.Fragments[1].Start != 10 || e.Fragments[1].End != 16 {
		t.Fatalf("%+v", e.Fragments)
	}
	text := render(t, r)
	wants(t, text, "launchWorker  ", "13\t\t// Detach: the worker gets a process group of its own.\n14\t\tsetDetached(cmd)\n")
	if strings.Contains(e.Fragments[1].Text, "func setDetached") {
		t.Fatalf("a fragment left its symbol:\n%s", e.Fragments[1].Text)
	}
}

func TestSearchFragmentsFollowTheContextFlags(t *testing.T) {
	c := launcherWorkspace(t)
	c.Context = query.Context{Set: true}
	e := entryOf(t, search(t, c, "start the worker detached", query.SearchOpts{}), "launchWorker")
	if len(e.Fragments) != 2 || e.Fragments[0].End != 6 || e.Fragments[1].Start != 12 || e.Fragments[1].End != 15 {
		t.Fatalf("-C 0 shows the matching lines alone: %+v", e.Fragments)
	}
	c.Context = query.Context{Symbol: true, Set: true}
	e = entryOf(t, search(t, c, "start the worker detached", query.SearchOpts{}), "launchWorker")
	if len(e.Fragments) != 1 || e.Fragments[0].Start != 5 || e.Fragments[0].End != 16 {
		t.Fatalf("--context symbol shows the symbol with its doc: %+v", e.Fragments)
	}
	c.Context = query.Context{Before: 1, After: 4, File: true, Set: true}
	e = entryOf(t, search(t, c, "start the worker detached", query.SearchOpts{}), "launchWorker")
	if last := e.Fragments[len(e.Fragments)-1]; last.End != 18 || !strings.Contains(last.Text, "func setDetached") {
		t.Fatalf("--context file lets a fragment leave its symbol: %+v", e.Fragments)
	}
}

// A keyword hit shows the lines holding the query terms; a file changed
// since it was indexed shows none and says so.
func TestKeywordHitsShowTheirLinesAndStaleFilesNone(t *testing.T) {
	c := launcherWorkspace(t)
	c.Embedder = nil
	e := entryOf(t, search(t, c, "worker binary", query.SearchOpts{}), "launchWorker")
	if len(e.Fragments) != 2 || e.Fragments[0].Start != 5 || e.Fragments[0].End != 7 ||
		!strings.Contains(e.Fragments[0].Text, "starts the worker binary") || e.Fragments[1].Start != 10 {
		t.Fatalf("%+v", e.Fragments)
	}
	t.Setenv("CODEDOC_FRESH_MAX_FILES", "0")
	writeAppend(t, filepath.Join(c.Workspace.Repos[0].Path, "store/launch.go"), "\n// later\n")
	later := &query.Ctx{Store: c.Store, Workspace: c.Workspace}
	e = entryOf(t, search(t, later, "worker binary", query.SearchOpts{}), "launchWorker")
	if len(e.Fragments) != 0 || !strings.Contains(strings.Join(e.Tags, " "), "stale") {
		t.Fatalf("%+v %v", e.Fragments, e.Tags)
	}
}

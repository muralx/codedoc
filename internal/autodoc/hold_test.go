package autodoc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/docs/write"
)

// hold runs RunHeld on the fixture with one key per group, so module
// a's two keys stack two journals on a/a.go before module b's group.
func hold(t *testing.T, f *fixture, edit func(*Options)) *Held {
	t.Helper()
	opts := f.options()
	opts.GroupSize = 1
	if edit != nil {
		edit(&opts)
	}
	h, err := RunHeld(context.Background(), opts, fakeGaps{list: testGaps}, f.idx, Git{}, f)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// journals lists the run directories left in the state directory.
func journals(t *testing.T) []string {
	t.Helper()
	list, _ := os.ReadDir(filepath.Join(os.Getenv("XDG_STATE_HOME"), "codedoc", "runs"))
	var names []string
	for _, e := range list {
		names = append(names, e.Name())
	}
	return names
}

// same fails the test when got differs from want.
func same[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: got %v, want %v", what, got, want)
	}
}

func TestRunHeldCommitsTheWholeRunOnce(t *testing.T) {
	f := newFixture(t, "ok")
	var ids []string
	h := hold(t, f, func(o *Options) { o.OnGroup = func(id string) { ids = append(ids, id) } })
	same(t, "summary", h.Summary, Summary{Documented: 3, Cost: 0.75})
	same(t, "interrupted", h.Interrupted, "")
	same(t, "keys", strings.Join(h.Keys, " "), "r:go:a:A1 r:go:a:A2 r:go:b:B1")
	same(t, "paths", strings.Join(h.Paths, " "), "a/a.go b/b.go")
	same(t, "groups", len(ids), 3)
	same(t, "journals are the groups, in order", strings.Join(journals(t), " "), strings.Join(ids, " "))
	same(t, "last id ends in its number", strings.HasSuffix(ids[2], "-003") && slices.IsSorted(ids), true)
	same(t, "commits before the decision", git(t, f.root, "rev-list", "--count", "HEAD"), "1")
	same(t, "writes before the decision", git(t, f.root, "status", "--porcelain"), "M a/a.go\n M b/b.go")
	if err := h.Commit("docs: document 3 changed symbols\n\nbody\n"); err != nil {
		t.Fatal(err)
	}
	same(t, "log", git(t, f.root, "log", "--format=%an <%ae>|%s", "--name-only"),
		"Repo Owner <owner@example.com>|docs: document 3 changed symbols\n\na/a.go\nb/b.go\n"+
			"Repo Owner <owner@example.com>|init\n\na/a.go\nb/b.go\ngo.mod")
	same(t, "status", git(t, f.root, "status", "--porcelain"), "")
	same(t, "journals left", len(journals(t)), 0)
}

func TestHeldKeepLeavesTheWritesUncommitted(t *testing.T) {
	f := newFixture(t, "ok")
	h := hold(t, f, nil)
	if err := h.Keep(); err != nil {
		t.Fatal(err)
	}
	if st := git(t, f.root, "status", "--porcelain"); st != "M a/a.go\n M b/b.go" || len(journals(t)) != 0 {
		t.Fatalf("status %q, journals %v", st, journals(t))
	}
}

func TestHeldRevertPutsBackEveryGroup(t *testing.T) {
	f := newFixture(t, "ok")
	h := hold(t, f, nil)
	if err := h.Revert(); err != nil {
		t.Fatal(err)
	}
	if st := git(t, f.root, "status", "--porcelain"); st != "" || len(journals(t)) != 0 {
		t.Fatalf("status %q, journals %v\n%s", st, journals(t), f.log)
	}
	if last := f.idx.reindex[len(f.idx.reindex)-1]; !slices.Equal(last, []string{"a/a.go", "b/b.go"}) {
		t.Fatalf("reverted files not re-indexed: %v", f.idx.reindex)
	}
}

func TestRunHeldStopsWhenSomeoneElseChangesTheTree(t *testing.T) {
	for mode, want := range map[string]string{
		"late-stray": "the working tree changed during the run: changed ROOT/go.mod, ROOT/new/stray.txt",
		"late-head":  "the working tree changed during the run: moved HEAD",
	} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t, mode)
			h := hold(t, f, nil)
			same(t, "interrupted", h.Interrupted, strings.ReplaceAll(want, "ROOT", f.root))
			same(t, "documented", h.Summary.Documented, 2)
			same(t, "failed", h.Summary.Failed, 1)
			same(t, "the interrupted group is restored", readFile(t, f.root, "b/b.go"), srcB)
			same(t, "the earlier groups are held", strings.Contains(readFile(t, f.root, "a/a.go"), "// A2 does things."), true)
			if err := h.Revert(); err != nil {
				t.Fatal(err)
			}
			same(t, "a.go after the revert", readFile(t, f.root, "a/a.go"), srcA)
			same(t, "journals left", len(journals(t)), 0)
			if mode == "late-head" {
				same(t, "someone else's commit", git(t, f.root, "log", "-1", "--format=%s"), "user work")
				return
			}
			same(t, "someone else's edit", readFile(t, f.root, "go.mod"), "module hacked\n")
			same(t, "someone else's file", readFile(t, f.root, "new/stray.txt"), "x\n")
		})
	}
}

func TestHeldRevertLeavesWhatSomeoneCommitted(t *testing.T) {
	f := newFixture(t, "late-commit-all")
	h := hold(t, f, nil)
	if !strings.Contains(h.Interrupted, "moved HEAD") {
		t.Fatalf("%q", h.Interrupted)
	}
	if err := h.Revert(); err != nil {
		t.Fatal(err)
	}
	if st := git(t, f.root, "status", "--porcelain"); st != "" || len(journals(t)) != 0 {
		t.Fatalf("the revert left changes nobody made: %q, journals %v", st, journals(t))
	}
	if !strings.Contains(readFile(t, f.root, "a/a.go"), "// A1 does things.") {
		t.Fatal("a committed doc was taken out of the working tree")
	}
}

func TestRunHeldRestoresAGroupThatFailsOnItsOwn(t *testing.T) {
	f := newFixture(t, "broken")
	h := hold(t, f, nil)
	if h.Interrupted != "" || h.Summary.Failed != 3 || len(h.Keys) != 0 || len(journals(t)) != 0 {
		t.Fatalf("%+v, journals %v", h, journals(t))
	}
	if st := git(t, f.root, "status", "--porcelain"); st != "" {
		t.Fatalf("status %q", st)
	}
	if err := h.Commit("docs: nothing"); err != nil || git(t, f.root, "rev-list", "--count", "HEAD") != "1" {
		t.Fatalf("an empty run committed: %v", err)
	}
}

func TestRunHeldSpendsAtMostItsBudget(t *testing.T) {
	f := newFixture(t, "ok")
	h := hold(t, f, func(o *Options) { o.BudgetUSD = 0.3 })
	if h.Summary != (Summary{Documented: 2, Skipped: 1, Cost: 0.5}) {
		t.Fatalf("%+v", h.Summary)
	}
	record := readFile(t, filepath.Dir(f.record), filepath.Base(f.record))
	if strings.Count(record, `"--max-budget-usd","0.30"`) != 1 || strings.Count(record, `"--max-budget-usd","0.05"`) != 1 {
		t.Fatalf("budgets passed to claude:\n%s", record)
	}
	if !strings.Contains(f.log.String(), "autodoc r: the run's budget of $0.30 is spent") {
		t.Fatalf("log:\n%s", f.log)
	}
	if err := h.Revert(); err != nil {
		t.Fatal(err)
	}
}

func TestRunHeldRefusals(t *testing.T) {
	f := newFixture(t, "ok")
	if _, err := RunHeld(context.Background(), f.options(), fakeGaps{err: errors.New("db")}, f.idx, Git{}, nil); err == nil {
		t.Fatal("a gaps error was ignored")
	}
	h := hold(t, f, nil)
	h.r.git = failingCommit{}
	if err := h.Commit("docs: x"); err == nil {
		t.Fatal("a failed commit was reported done")
	}
	if err := h.Revert(); err != nil || readFile(t, f.root, "a/a.go") != srcA {
		t.Fatalf("a run whose commit failed cannot be reverted: %v", err)
	}
	writeFile(t, f.root, "notes.txt", "mine\n")
	if _, err := RunHeld(context.Background(), f.options(), fakeGaps{list: testGaps}, f.idx, Git{}, f); !errors.Is(err, ErrDirty) {
		t.Fatalf("got %v, want ErrDirty", err)
	}
}

func TestLeftoversOfAHeldRunAreRestoredNewestFirst(t *testing.T) {
	f := newFixture(t, "ok")
	deadRun(t, f.root, "autodoc-20261009T120000-abcd-001", "r:go:a:A1")
	deadRun(t, f.root, "autodoc-20261009T120000-abcd-002", "r:go:a:A2")
	RecoverLeftovers(f)
	if got := readFile(t, f.root, "a/a.go"); got != srcA || len(journals(t)) != 0 {
		t.Fatalf("journals %v, a.go:\n%s\nlog:\n%s", journals(t), got, f.log)
	}
	if _, err := write.Files("autodoc-20261009T120000-abcd-001"); err != nil {
		t.Fatal(err)
	}
}

package upkeep_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/docs/write"
	"github.com/muralx/codedoc/internal/gitmeta"
	"github.com/muralx/codedoc/internal/upkeep"
)

func TestEvaluateTruthTable(t *testing.T) {
	f := newFixture(t)
	head := f.add("Alpha")
	ready := func(*fixture, *upkeep.Env) {}
	for _, tc := range []struct {
		name  string
		setup func(*fixture, *upkeep.Env)
		wait  string
		ready bool
	}{
		{"ready", ready, "", true},
		{"disabled", func(f *fixture, _ *upkeep.Env) { f.ws.Upkeep.Enabled = false }, upkeep.WaitOff, false},
		{"not quiet", func(f *fixture, e *upkeep.Env) { e.LastEvent = f.now.Add(-4 * time.Minute) }, upkeep.WaitQuiet, false},
		{"quiet just long enough", func(f *fixture, e *upkeep.Env) { e.LastEvent = f.now.Add(-5 * time.Minute) }, "", true},
		{"untracked file", func(f *fixture, _ *upkeep.Env) { f.write("notes.txt", "mine\n") }, upkeep.WaitDirty, false},
		{"ignored file", func(f *fixture, _ *upkeep.Env) { f.write(".git/info/exclude", "*.log\n"); f.write("x.log", "x\n") }, "", true},
		{"modified file", func(f *fixture, _ *upkeep.Env) { f.write("go.mod", "module example.com/lib\n") }, upkeep.WaitDirty, false},
		{"staged file", func(f *fixture, _ *upkeep.Env) { f.write("s.txt", "s\n"); f.git("add", "s.txt") }, upkeep.WaitDirty, false},
		{"detached HEAD", func(f *fixture, _ *upkeep.Env) { f.git("checkout", "-q", "--detach") }, upkeep.WaitDetached, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := f.env()
			tc.setup(f, &e)
			c, err := upkeep.Evaluate(e)
			if err != nil || c.Wait != tc.wait || c.Ready() != tc.ready {
				t.Fatalf("%+v %v", c, err)
			}
			if tc.ready && (keys(c.Pending) != "Alpha" || c.Branch != "main" || c.Head != head) {
				t.Fatalf("%+v", c)
			}
			f.ws.Upkeep.Enabled = true
			f.git("checkout", "-q", "main")
			f.git("reset", "-q", "--hard")
			f.git("clean", "-fdq")
		})
	}
	if f.state("main") != "" {
		t.Fatalf("a check with candidates moved the state to %q", f.state("main"))
	}
}

func TestEvaluateRecordsACleanBranchWithNothingPending(t *testing.T) {
	f := newFixture(t)
	if c := f.check(); c.Wait != "" || c.Ready() || f.state("main") != c.Head {
		t.Fatalf("%+v, state %q", c, f.state("main"))
	}
	f.write("documented.go", "package lib\n\n// Documented does things.\nfunc Documented() int {\n\treturn 1\n}\n")
	f.write("lib_test.go", "package lib\n\nfunc helper() int {\n\treturn 1\n}\n")
	f.git("add", "-A")
	f.git("commit", "-qm", "nothing to document")
	if c := f.check(); c.Ready() || f.state("main") != f.git("rev-parse", "HEAD") {
		t.Fatalf("documented and test code are no candidates: %+v", c)
	}
}

func TestEvaluateFirstRestoresWhatADeadRunLeft(t *testing.T) {
	f := newFixture(t)
	f.add("Alpha")
	before := f.read("alpha.go")
	const id = "autodoc-20261009T110000-dead-001"
	t.Setenv(write.RunIDEnv, id)
	if _, err := write.Apply(write.Target{Root: f.root, Path: "alpha.go", Lang: "go", Qualified: "Alpha"}, "Alpha is half done."); err != nil {
		t.Fatal(err)
	}
	dir, err := write.RunDir(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "autodoc.running"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(write.RunIDEnv, "")
	same(f, "the dead run's write", f.git("status", "--porcelain"), "M alpha.go")
	c := f.check()
	same(f, "wait", c.Wait, "")
	same(f, "pending", keys(c.Pending), "Alpha")
	same(f, "alpha.go", f.read("alpha.go"), before)
}

func TestFirstRunLooksAtTheLastCommitsOnly(t *testing.T) {
	f := newFixture(t)
	alpha := f.add("Alpha")
	f.add("Beta")
	f.add("Gamma")
	if c := f.check(); keys(c.Pending) != "Alpha Beta Gamma" || f.state("main") != "" {
		t.Fatalf("last 20 commits, all there are: %q, state %q", keys(c.Pending), f.state("main"))
	}
	f.ws.Upkeep.FirstRunCommits = 2
	if c := f.check(); keys(c.Pending) != "Beta Gamma" || f.state("main") != alpha {
		t.Fatalf("last 2 commits: %q, state %q, want the commit they started from", keys(c.Pending), f.state("main"))
	}
	f.git("commit", "-q", "--allow-empty", "-m", "one more")
	if c := f.check(); keys(c.Pending) != "Beta Gamma" {
		t.Fatalf("a later check looks at the same commits and the new one: %q", keys(c.Pending))
	}
}

func TestOnlyTheBranchsOwnCommitsAreCandidates(t *testing.T) {
	f := newFixture(t)
	f.git("checkout", "-q", "-b", "work")
	f.add("Work")
	f.git("checkout", "-q", "main")
	f.add("Main")
	f.git("checkout", "-q", "work")
	f.git("merge", "-q", "--no-edit", "main")
	if c := f.check(); keys(c.Pending) != "Work" || c.Branch != "work" {
		t.Fatalf("on work, with main merged in: %q", keys(c.Pending))
	}
	f.git("checkout", "-q", "main")
	if c := f.check(); keys(c.Pending) != "Main" {
		t.Fatalf("on main: %q", keys(c.Pending))
	}
}

func TestStateIsKeptPerBranchAcrossSwitches(t *testing.T) {
	f := newFixture(t)
	f.add("Alpha")
	main := f.run().Commit
	f.git("checkout", "-q", "-b", "work")
	if f.state("main") != main || f.state("work") != "" {
		t.Fatalf("a branch switch changed the state: main %q work %q", f.state("main"), f.state("work"))
	}
	f.add("Work")
	work := f.run().Commit
	f.git("checkout", "-q", "main")
	if c := f.check(); c.Ready() || f.state("main") != main || f.state("work") != work {
		t.Fatalf("back on main: %+v, main %q work %q", c, f.state("main"), f.state("work"))
	}
	f.add("Beta")
	f.git("checkout", "-q", "work")
	if c := f.check(); c.Ready() || f.state("work") != work {
		t.Fatalf("back on work, main moved on: %+v", c)
	}
	f.git("checkout", "-q", "main")
	if c := f.check(); keys(c.Pending) != "Beta" {
		t.Fatalf("main since its last documented commit: %q", keys(c.Pending))
	}
}

func TestARewrittenBranchFallsBackToTheFirstRunRule(t *testing.T) {
	f := newFixture(t)
	lost := f.add("Alpha")
	row, err := f.store.RepoByPath(f.root)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.SetUpkeepBranch(row.ID, "main", lost, 1); err != nil {
		t.Fatal(err)
	}
	if c := f.check(); c.Ready() {
		t.Fatalf("documented up to the head: %+v", c)
	}
	f.git("commit", "-q", "--amend", "-m", "rewritten")
	if ok, err := gitmeta.IsAncestor(f.root, lost, "HEAD"); ok || err != nil {
		t.Fatalf("the amended commit is still an ancestor: %v", err)
	}
	if c := f.check(); keys(c.Pending) != "Alpha" || f.state("main") != lost {
		t.Fatalf("after the rewrite: %q, state %q", keys(c.Pending), f.state("main"))
	}
}

// brokenGit fails where its fields say.
type brokenGit struct {
	upkeep.CLI
	head  gitmeta.HeadState
	fail  string
	dirty bool
}

func (g brokenGit) Head(root string) (gitmeta.HeadState, error) {
	if g.fail == "head" {
		return gitmeta.HeadState{}, errors.New("head broke")
	}
	if g.head != (gitmeta.HeadState{}) {
		return g.head, nil
	}
	return g.CLI.Head(root)
}

func (g brokenGit) Dirty(string) (bool, error) {
	if g.fail == "dirty" {
		return false, errors.New("status broke")
	}
	return g.dirty, nil
}

func (g brokenGit) Scope(root, branch, head, last string, n int) (upkeep.Scope, error) {
	if g.fail == "scope" {
		return upkeep.Scope{}, errors.New("scope broke")
	}
	return g.CLI.Scope(root, branch, head, last, n)
}

func TestEvaluateReportsWhatItCannotLookAt(t *testing.T) {
	f := newFixture(t)
	f.add("Alpha")
	for _, fail := range []string{"head", "dirty", "scope"} {
		e := f.env()
		e.Git = brokenGit{fail: fail}
		if _, err := upkeep.Evaluate(e); err == nil {
			t.Errorf("%s: no error", fail)
		}
	}
	e := f.env()
	e.Git = brokenGit{head: gitmeta.HeadState{Branch: "main"}}
	if c, err := upkeep.Evaluate(e); c.Wait != upkeep.WaitUnborn || err != nil {
		t.Fatalf("%+v %v", c, err)
	}
	e = f.env()
	e.Repo = &config.Repo{Name: "plain", Path: t.TempDir()}
	if c, err := upkeep.Evaluate(e); c.Wait != upkeep.WaitNoGit || err != nil {
		t.Fatalf("%+v %v", c, err)
	}
	e.Repo.Path = filepath.Join(f.root, "sub")
	if err := os.Mkdir(e.Repo.Path, 0o755); err != nil {
		t.Fatal(err)
	}
	e.Git = brokenGit{}
	if c, err := upkeep.Evaluate(e); c.Wait != upkeep.WaitNoIndex || err != nil {
		t.Fatalf("%+v %v", c, err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := upkeep.Evaluate(f.env()); err == nil {
		t.Fatal("a closed database answered")
	}
}

func TestEnvDefaults(t *testing.T) {
	f := newFixture(t)
	e := f.env()
	e.Git, e.Now, e.Skip, e.Log, e.LastEvent = nil, nil, nil, nil, time.Time{}
	if c, err := upkeep.Evaluate(e); err != nil || c.Wait != "" || c.Branch != "main" {
		t.Fatalf("%+v %v", c, err)
	}
}

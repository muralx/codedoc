package upkeep_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/muralx/codedoc/internal/autodoc"
	"github.com/muralx/codedoc/internal/upkeep"
)

func TestRunCommitsTheDocsOnceAndAdvancesTheBranch(t *testing.T) {
	f := newFixture(t)
	f.add("Alpha")
	head := f.add("Beta")
	res := f.run()
	same(f, "summary", res.Summary, autodoc.Summary{Documented: 2, Cost: 0.25})
	same(f, "interrupted", res.Interrupted, "")
	same(f, "left", res.Left, 0)
	same(f, "documented", strings.Join(res.Documented, " "), libKey+"Alpha "+libKey+"Beta")
	same(f, "spent today", res.SpentToday, 0.25)
	same(f, "the one docs commit", f.git("log", "--format=%H|%an <%ae>|%s|%b", "-1"),
		res.Commit+"|Repo Owner <owner@example.com>|docs: document 2 changed symbols|"+libKey+"Alpha\n"+libKey+"Beta")
	same(f, "its parent", f.git("rev-parse", "HEAD~1"), head)
	same(f, "its files", f.git("show", "--name-only", "--format=", "HEAD"), "alpha.go\nbeta.go")
	same(f, "alpha.go", f.read("alpha.go"), "package lib\n\n// Alpha does things.\nfunc Alpha() int {\n\treturn 1\n}\n")
	same(f, "status", f.git("status", "--porcelain"), "")
	same(f, "last documented commit", f.state("main"), res.Commit)
	c := f.check()
	same(f, "wait after the run", c.Wait, "")
	same(f, "pending after the run", len(c.Pending), 0)
	same(f, "spent after the run", c.SpentToday, 0.25)
}

func TestRunWithoutCommitLeavesTheDocsInTheTree(t *testing.T) {
	f := newFixture(t)
	head := f.add("Alpha")
	no := false
	f.ws.Upkeep.Commit = &no
	res := f.run()
	if res.Commit != "" || f.git("rev-parse", "HEAD") != head || f.git("status", "--porcelain") != "M alpha.go" {
		t.Fatalf("%+v, status %q", res, f.git("status", "--porcelain"))
	}
	if f.state("main") != head {
		t.Fatalf("last documented %s, want the head the candidates came from", f.state("main"))
	}
	if c := f.check(); c.Wait != upkeep.WaitDirty {
		t.Fatalf("%+v", c)
	}
	f.git("commit", "-qam", "docs by hand")
	if c := f.check(); c.Wait != "" || len(c.Pending) != 0 || f.state("main") != f.git("rev-parse", "HEAD") {
		t.Fatalf("after committing the docs: %+v", c)
	}
}

func TestRunIsRevertedAndRetriedWhenTheTreeChangesUnderIt(t *testing.T) {
	for mode, tc := range map[string]struct {
		why, status string
		undo        []string
	}{
		"edit": {"the working tree changed during the run: changed ROOT/scratch.txt", "?? scratch.txt", []string{"clean", "-fdq"}},
		"head": {"the working tree changed during the run: moved HEAD", "", []string{"status"}},
	} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			f.add("Alpha")
			before := f.read("alpha.go")
			t.Setenv(stubEnv, mode)
			res := f.run()
			same(f, "interrupted", res.Interrupted, strings.ReplaceAll(tc.why, "ROOT", f.root))
			same(f, "commit", res.Commit, "")
			same(f, "spent today", res.SpentToday, 0.25)
			same(f, "alpha.go", f.read("alpha.go"), before)
			same(f, "last documented commit", f.state("main"), "")
			same(f, "status: only what someone else did", f.git("status", "--porcelain"), tc.status)
			f.git(tc.undo...)
			t.Setenv(stubEnv, "ok")
			res = f.run()
			same(f, "retry interrupted", res.Interrupted, "")
			same(f, "retry advanced to its commit", f.state("main"), res.Commit)
			same(f, "retry committed", res.Commit != "", true)
		})
	}
}

func TestRunCancelledIsReverted(t *testing.T) {
	f := newFixture(t)
	f.add("Alpha")
	c := f.check()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := upkeep.Run(ctx, f.env(), c)
	if err != nil || res.Interrupted != "cancelled" || f.state("main") != "" || f.git("status", "--porcelain") != "" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestRunStopsAtMaxItemsAndGoesOnNextTime(t *testing.T) {
	f := newFixture(t)
	f.add("Alpha")
	f.add("Beta")
	f.ws.Upkeep.MaxItems = 1
	res := f.run()
	if len(res.Documented) != 1 || res.Left != 1 || f.state("main") != "" {
		t.Fatalf("%+v, state %q", res, f.state("main"))
	}
	if c := f.check(); len(c.Pending) != 1 {
		t.Fatalf("second check: %+v", c)
	}
	if res = f.run(); res.Left != 0 || f.state("main") != res.Commit || f.git("rev-list", "--count", "HEAD") != "5" {
		t.Fatalf("second run: %+v", res)
	}
}

func TestRunSpendsAtMostItsCapAndStopsForTheDayAtTheDailyCap(t *testing.T) {
	f := newFixture(t)
	f.add("Alpha")
	f.add("Beta")
	f.ws.Autodoc.GroupSize = 1
	f.ws.Upkeep.MaxCostUSD, f.ws.Upkeep.DailyCostUSD = 0.25, 0.5
	res := f.run()
	same(f, "first run, capped at $0.25", res.Summary, autodoc.Summary{Documented: 1, Skipped: 1, Cost: 0.25})
	same(f, "left", res.Left, 1)
	same(f, "spent", res.SpentToday, 0.25)
	res = f.run()
	same(f, "left after the second run", res.Left, 0)
	same(f, "spent after the second run", res.SpentToday, 0.5)
	f.add("Gamma")
	c := f.check()
	same(f, "wait at the daily cap", c.Wait, upkeep.WaitCap)
	same(f, "ready at the daily cap", c.Ready(), false)
	same(f, "spent at the daily cap", c.SpentToday, 0.5)
	f.now = f.now.Add(24 * time.Hour)
	c = f.check()
	same(f, "wait the next day", c.Wait, "")
	same(f, "pending the next day", keys(c.Pending), "Gamma")
	same(f, "spent the next day", c.SpentToday, 0.0)
}

func TestRunSkipsWhatItFailedOnUntilTheBranchAdvances(t *testing.T) {
	f := newFixture(t)
	head := f.add("Alpha")
	t.Setenv(stubEnv, "idle")
	res := f.run()
	if res.Summary.Failed != 1 || len(res.Documented) != 0 || res.Left != 0 || res.Commit != "" {
		t.Fatalf("%+v", res)
	}
	if f.state("main") != head || len(f.skip) != 0 {
		t.Fatalf("state %q, skip %v", f.state("main"), f.skip)
	}
	f.add("Beta")
	f.ws.Autodoc.GroupSize = 1
	t.Setenv(stubEnv, "ok")
	if c := f.check(); keys(c.Pending) != "Beta" {
		t.Fatalf("only what changed since: %+v", c)
	}
}

func TestRunCountsAClaudeRunWithoutACostAtItsBudget(t *testing.T) {
	f := newFixture(t)
	f.add("Alpha")
	c := f.check()
	e := f.env()
	e.ClaudeBin = "/nonexistent/claude"
	res, err := upkeep.Run(context.Background(), e, c)
	if err != nil || res.Summary.UnknownCost != 1 || res.SpentToday != 1 || res.Summary.Failed != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if spent, _ := f.store.UpkeepSpent(upkeep.Day(f.now)); spent != 1 {
		t.Fatalf("spent %v", spent)
	}
}

// A claude run of upkeep may spend the whole budget of the upkeep run,
// not the per-run cap of autodoc, and runs on the model of the upkeep
// section.
func TestRunGivesClaudeTheRunBudgetAndTheModel(t *testing.T) {
	f := newFixture(t)
	f.add("Alpha")
	f.ws.Autodoc.MaxCostUSD = 0.10
	f.ws.Upkeep.MaxCostUSD, f.ws.Upkeep.Model = 3, "haiku"
	f.run()
	rec, err := os.ReadFile(f.record)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rec), "--max-budget-usd 3.00 --model haiku") {
		t.Fatalf("claude ran as:\n%s", rec)
	}
}

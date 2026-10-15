package upkeep

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/muralx/codedoc/internal/autodoc"
)

// Result is what one Run did.
type Result struct {
	// Summary counts the candidates by outcome and the reported cost.
	Summary autodoc.Summary
	// Documented are the keys whose docs were written and kept.
	Documented []string
	// Interrupted says why the run was undone: the working tree stopped
	// being clean or quiet under it. Everything the run wrote was put
	// back and the branch's state did not move; "" otherwise.
	Interrupted string
	// Commit is the docs commit the run made; "" when it made none.
	Commit string
	// Left counts the candidates still pending: those beyond max_items
	// or the budget. The branch's state advances only when it is 0.
	Left int
	// SpentToday is the workspace's spend of the day after the run.
	SpentToday float64
}

// fixed serves a check's candidates to the runner.
type fixed []autodoc.Gap

// Next returns the candidates; the runner applies its own limit.
func (f fixed) Next(string, int) ([]autodoc.Gap, error) { return f, nil }

// Run documents the candidates of a ready check, at most max_items of
// them, spending at most max_cost_usd and what is left of the daily
// cap. What it spent is added to the day's spend whatever happens next;
// a claude run that ended without reporting its cost counts at the
// budget it had. When the working tree changed under the run (a file
// outside the run's own writes, a moved HEAD) or ctx was cancelled, the
// run is reverted as a whole and reported Interrupted. Otherwise its
// docs are committed as one commit (or left in the tree when commit is
// false), and the branch's last documented commit advances once no
// candidate is left: to the docs commit, or without one to the head the
// candidates were computed from. Keys a group failed on are added to
// Env.Skip.
func Run(ctx context.Context, e Env, c Check) (Result, error) {
	e = e.withDefaults()
	u := e.Workspace.Upkeep
	opts := autodoc.Options{Repo: e.Repo.Name, Root: e.Repo.Path, MaxItems: u.MaxItems, MaxMinutes: e.MaxMinutes,
		GroupSize: e.Workspace.Autodoc.GroupSize, MaxCostUSD: -1, Model: u.Model,
		BudgetUSD: min(u.MaxCostUSD, u.DailyCostUSD-c.SpentToday), ClaudeBin: e.ClaudeBin, Now: e.Now, OnGroup: e.OnGroup}
	h, err := autodoc.RunHeld(ctx, opts, fixed(c.Pending), autodoc.Indexed{Ctx: e.query()}, e.Git, e.Log)
	res := Result{SpentToday: c.SpentToday}
	if errors.Is(err, autodoc.ErrDirty) {
		res.Interrupted = WaitDirty
		return res, nil
	}
	if h != nil {
		res.Summary = h.Summary
		err = errors.Join(err, e.spend(&res, h.Summary.Cost+h.Unreported))
	}
	if err != nil {
		return res, err
	}
	if h.Interrupted != "" || ctx.Err() != nil {
		res.Interrupted = cmp.Or(h.Interrupted, "cancelled")
		return res, h.Revert()
	}
	return res, e.finish(&res, c, h)
}

// spend adds usd to the day's spend.
func (e Env) spend(res *Result, usd float64) error {
	if usd <= 0 {
		return nil
	}
	res.SpentToday += usd
	return e.Store.AddUpkeepSpend(Day(e.Now()), usd)
}

// finish commits or keeps what the run wrote and records the branch's
// state: advanced when no candidate is left, else only the time of the
// run.
func (e Env) finish(res *Result, c Check, h *autodoc.Held) error {
	commit := c.Head
	switch {
	case len(h.Keys) == 0:
	case !e.Workspace.Upkeep.Commits():
		if err := h.Keep(); err != nil {
			return err
		}
	default:
		if err := h.Commit(message(h.Keys)); err != nil {
			return errors.Join(err, h.Revert())
		}
		head, err := e.Git.Head(e.Repo.Path)
		if err != nil {
			return err
		}
		commit, res.Commit = head.Commit, head.Commit
	}
	res.Documented = h.Keys
	for _, k := range h.Failed {
		e.Skip[k] = true
	}
	for _, g := range c.Pending {
		if !slices.Contains(h.Keys, g.Key) && !e.Skip[g.Key] {
			res.Left++
		}
	}
	if res.Left > 0 {
		return e.Store.SetUpkeepBranch(c.repoID, c.Branch, c.last.Commit, e.Now().Unix())
	}
	return e.advance(&c, commit, e.Now().Unix())
}

// message is the commit message of a run: a docs: subject and the
// documented keys, one per line.
func message(keys []string) string {
	noun := "symbols"
	if len(keys) == 1 {
		noun = "symbol"
	}
	return fmt.Sprintf("docs: document %d changed %s\n\n%s\n", len(keys), noun, strings.Join(keys, "\n"))
}

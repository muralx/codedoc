// Package upkeep keeps documentation current outside any session
// (efficiency spec section 7): once a repo is clean and quiet on a
// branch, the symbols that branch changed since its last documented
// commit whose doc is stale or missing are documented by the autodoc
// runner, as one unit that is committed, kept or reverted whole. The
// daemon calls Evaluate on its tick and Run when the answer is ready.
package upkeep

import (
	"errors"
	"time"

	"github.com/muralx/codedoc/internal/autodoc"
	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/embed"
	"github.com/muralx/codedoc/internal/gitmeta"
	"github.com/muralx/codedoc/internal/query"
	"github.com/muralx/codedoc/internal/store"
)

// Why a repo waits (Check.Wait).
const (
	WaitOff      = "off"
	WaitCap      = "daily cap reached"
	WaitQuiet    = "waiting for quiet"
	WaitNoGit    = "not a git repository"
	WaitDetached = "HEAD is not on a branch"
	WaitUnborn   = "no commit yet"
	WaitDirty    = "uncommitted changes"
	WaitNoIndex  = "not indexed"
)

// minBudget is the least budget a run starts with, in USD.
const minBudget = 0.01

// Env is one repo under upkeep and what a check or a run of it needs.
type Env struct {
	// Store is the workspace database; it holds the per-branch state
	// and the daily spend.
	Store     *store.Store
	Workspace *config.Workspace
	Repo      *config.Repo
	// Embedder, when set, embeds the vectors of re-indexed files.
	Embedder embed.Embedder
	// Git is the git access; default CLI{}.
	Git Git
	// Now is the clock; default time.Now.
	Now func() time.Time
	// LastEvent is when the repo's files last changed, as far as the
	// caller saw: the quiet time counts from it.
	LastEvent time.Time
	// Skip holds the keys a run failed to document; they are no
	// candidates until the branch's state advances, which clears it.
	// The caller keeps it between calls.
	Skip map[string]bool
	// Log receives one line per event; nil discards them.
	Log autodoc.Logger
	// ClaudeBin, MaxMinutes and OnGroup are passed to the runner (see
	// autodoc.Options).
	ClaudeBin  string
	MaxMinutes int
	OnGroup    func(id string)
}

// withDefaults fills the optional fields.
func (e Env) withDefaults() Env {
	if e.Git == nil {
		e.Git = CLI{}
	}
	if e.Now == nil {
		e.Now = time.Now
	}
	if e.Skip == nil {
		e.Skip = map[string]bool{}
	}
	if e.Log == nil {
		e.Log = discard{}
	}
	return e
}

// discard is the Logger of an Env without one.
type discard struct{}

func (discard) Printf(string, ...any) {}

// query returns a context over the workspace index. Each check and run
// takes its own: a context remembers that it found a repo fresh.
func (e Env) query() *query.Ctx {
	return &query.Ctx{Store: e.Store, Workspace: e.Workspace, Embedder: e.Embedder, Now: e.Now}
}

// Day names the day upkeep spending is counted under: the local date of
// t, so the daily cap starts over at local midnight.
func Day(t time.Time) string { return t.Format(time.DateOnly) }

// Check is the answer of Evaluate.
type Check struct {
	// Wait says why the repo is not ready; "" when it is, or when
	// nothing is pending.
	Wait string
	// Branch and Head are what is checked out; empty when Evaluate
	// stopped before looking.
	Branch, Head string
	// Pending are the candidates in gap rank order: the symbols and
	// modules the branch changed since its last documented commit
	// whose doc is stale or missing.
	Pending []autodoc.Gap
	// SpentToday is what the workspace's runs spent today, in USD.
	SpentToday float64

	repoID int64
	last   store.UpkeepBranch
}

// Ready reports whether a run should start.
func (c Check) Ready() bool { return c.Wait == "" && len(c.Pending) > 0 }

// Evaluate tells whether the repo is ready for a run: upkeep is
// enabled, today's spend is below the daily cap, no file changed for
// the quiet time, HEAD is on a branch, the working tree is clean, and
// the branch has candidates. It looks in that order and stops at the
// first that fails, naming it in Wait. Before judging the tree clean it
// restores what a run that died left behind. A clean branch without
// candidates is recorded as documented up to its head, so the next
// check has nothing to compare.
func Evaluate(e Env) (Check, error) {
	e = e.withDefaults()
	u := e.Workspace.Upkeep
	var c Check
	if !u.Enabled {
		c.Wait = WaitOff
		return c, nil
	}
	var err error
	if c.SpentToday, err = e.Store.UpkeepSpent(Day(e.Now())); err != nil {
		return c, err
	}
	switch {
	case u.DailyCostUSD-c.SpentToday < minBudget:
		c.Wait = WaitCap
	case e.Now().Sub(e.LastEvent) < u.Quiet.D():
		c.Wait = WaitQuiet
	default:
		err = e.inspect(&c)
	}
	return c, err
}

// inspect looks at the working tree: its head, then whether it is
// clean, then the candidates.
func (e Env) inspect(c *Check) error {
	head, err := e.Git.Head(e.Repo.Path)
	switch {
	case errors.Is(err, gitmeta.ErrNotRepo):
		c.Wait = WaitNoGit
		return nil
	case err != nil:
		return err
	case head.Branch == "HEAD":
		c.Wait = WaitDetached
		return nil
	case head.Commit == "":
		c.Wait = WaitUnborn
		return nil
	}
	c.Branch, c.Head = head.Branch, head.Commit
	autodoc.RecoverLeftovers(e.Log)
	dirty, err := e.Git.Dirty(e.Repo.Path)
	if err != nil {
		return err
	}
	if dirty {
		c.Wait = WaitDirty
		return nil
	}
	return e.candidates(c)
}

// candidates fills c.Pending from the branch's scope. It records the
// head as documented when there are none, else the anchor of a scope
// selected by the first-run rule.
func (e Env) candidates(c *Check) error {
	row, err := e.Store.RepoByPath(e.Repo.Path)
	if errors.Is(err, store.ErrNotFound) {
		c.Wait = WaitNoIndex
		return nil
	}
	if err != nil {
		return err
	}
	c.repoID = row.ID
	if c.last, _, err = e.Store.UpkeepBranch(row.ID, c.Branch); err != nil || c.last.Commit == c.Head {
		return err
	}
	scope, err := e.Git.Scope(e.Repo.Path, c.Branch, c.Head, c.last.Commit, e.Workspace.Upkeep.FirstRunCommits)
	if err != nil {
		return err
	}
	if c.Pending, err = e.pending(scope); err != nil {
		return err
	}
	if len(c.Pending) == 0 {
		return e.advance(c, c.Head, c.last.RanAt)
	}
	if scope.Anchor == "" {
		return nil
	}
	c.last.Commit = scope.Anchor
	return e.Store.SetUpkeepBranch(c.repoID, c.Branch, scope.Anchor, c.last.RanAt)
}

// pending returns the gaps of the repo, in rank order, whose symbol or
// module changed in the scope and that no earlier run failed on.
func (e Env) pending(scope Scope) ([]autodoc.Gap, error) {
	if scope.Base == "" {
		return nil, nil
	}
	in := map[string]bool{}
	for _, p := range scope.Paths {
		in[p] = true
	}
	q := e.query()
	changed, err := query.ChangedKeys(q, e.Repo, scope.Base, func(p string) bool { return in[p] })
	if err != nil || len(changed) == 0 {
		return nil, err
	}
	gaps, err := autodoc.Indexed{Ctx: q}.Next(e.Repo.Name, 0)
	if err != nil {
		return nil, err
	}
	var out []autodoc.Gap
	for _, g := range gaps {
		if changed[g.Key] && !e.Skip[g.Key] {
			out = append(out, g)
		}
	}
	return out, nil
}

// advance records commit as the branch's last documented commit, with
// the Unix time at of its last run, and forgets the keys runs failed on.
func (e Env) advance(c *Check, commit string, at int64) error {
	clear(e.Skip)
	c.last = store.UpkeepBranch{Commit: commit, RanAt: at}
	return e.Store.SetUpkeepBranch(c.repoID, c.Branch, commit, at)
}

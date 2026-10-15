// Package autodoc documents gaps unattended (spec section 11.2): it hands
// each module's top gaps to a headless `claude -p` run that writes docs
// through `codedoc doc set`, checks the result, restores the run's files
// from the write journal on failure, and optionally commits exactly the
// files the run wrote.
package autodoc

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// StateOK is the doc state of a documented symbol whose doc is current.
const StateOK = "ok"

// ErrDirty reports a refusal to run on a working tree with uncommitted
// changes.
var ErrDirty = errors.New("autodoc: working tree has uncommitted changes (use --allow-dirty)")

// Gap is one undocumented or stale symbol or module, as ranked by the
// gaps queue.
type Gap struct {
	Key    string // stable key
	Module string // module the symbol belongs to; groups a run
	Path   string // repo-relative file
	Line   int
	State  string // missing or stale
	Score  float64
}

// Gaps yields a repo's documentation gaps in rank order, at most limit
// of them (all when limit <= 0).
type Gaps interface {
	Next(repo string, limit int) ([]Gap, error)
}

// Indexer re-indexes files and reports doc states.
type Indexer interface {
	// Reindex re-extracts the repo-relative paths of repo.
	Reindex(repo string, paths []string) error
	// States returns the doc state (ok, missing, stale, ...) of each
	// stable key; a key it does not know is absent.
	States(keys []string) (map[string]string, error)
	// Docs returns every symbol and module of repo by stable key
	// (repo:dir/ for a module), with a hash of its doc and the
	// repo-relative file the doc lives in.
	Docs(repo string) (map[string]DocAt, error)
}

// DocAt is where a key's doc lives and a hash of its text ("" when
// the key has no doc).
type DocAt struct {
	Path, Hash string
}

// Committer inspects and commits a working tree.
type Committer interface {
	// Dirty reports uncommitted changes or untracked files under root.
	Dirty(root string) (bool, error)
	// Commit commits exactly the repo-relative paths with message msg.
	Commit(root string, paths []string, msg string) error
}

// Logger receives one line per event; *daemon.Logger satisfies it.
type Logger interface {
	Printf(format string, args ...any)
}

// Options configure one autodoc run.
type Options struct {
	Repo string // repo name in the index
	Root string // absolute repo root, the agent's working directory
	// MaxItems caps the gaps taken; <= 0 means no cap.
	MaxItems int
	// MaxMinutes bounds the run's wall time; <= 0 means no bound. A
	// group still running at the bound is stopped and restored.
	MaxMinutes int
	// Commit commits each successful group's files.
	Commit bool
	// AllowDirty runs on a dirty working tree and disables Commit.
	AllowDirty bool
	// GroupSize caps the keys of one claude run; <= 0 means
	// DefaultGroupSize. A module with more gaps takes several runs.
	GroupSize int
	// MaxCostUSD caps the spend of one claude run (--max-budget-usd);
	// 0 means DefaultMaxCostUSD, a negative value no cap.
	MaxCostUSD float64
	// BudgetUSD caps the spend of the whole run: each claude run may
	// spend what is left of it, at most MaxCostUSD, and no group starts
	// once less than a cent is left. 0 means no cap.
	BudgetUSD float64
	// Model is the model of the claude runs (--model); empty leaves the
	// choice to claude.
	Model string
	// OnGroup, when set, is told the journal run id of each group before
	// its claude run starts; write.Files lists what that run wrote.
	OnGroup func(id string)
	// ClaudeBin is the claude binary; default $CODEDOC_CLAUDE_BIN, else
	// "claude" on PATH.
	ClaudeBin string
	// Now is the clock; default time.Now.
	Now func() time.Time
}

// DefaultGroupSize is the most keys one claude run documents.
const DefaultGroupSize = 5

// DefaultMaxCostUSD is the most one claude run may spend.
const DefaultMaxCostUSD = 1.00

// Summary counts the run's gaps by outcome and its total cost in USD.
type Summary struct {
	Documented int
	Skipped    int
	Failed     int
	Cost       float64
	// UnknownCost counts the claude runs that reported no cost, because
	// they timed out, were cancelled or crashed; Cost leaves them out.
	UnknownCost int
}

// String formats s as the runner's closing line.
func (s Summary) String() string {
	line := fmt.Sprintf("documented %d, skipped %d, failed %d, cost $%.4f", s.Documented, s.Skipped, s.Failed, s.Cost)
	if s.UnknownCost > 0 {
		line += fmt.Sprintf(" plus unknown (%d claude run(s) timed out or crashed before reporting a cost)", s.UnknownCost)
	}
	return line
}

// Run first restores the journals of earlier runs that died midway (see
// RecoverLeftovers), then documents the repo's top gaps, one module
// group at a time, and stops at MaxItems, MaxMinutes, cancellation of
// ctx, or an empty queue.
// A group fails when its keys are not all ok afterwards, or the agent
// changed anything besides their docs through codedoc doc set: its
// journaled files are restored from the journal (never with git
// checkout), files that were clean before it and changed otherwise are
// put back to HEAD, and its gaps count as failed. Run returns an error
// when it cannot proceed at all, or must stop because the agent changed
// something it cannot undo safely (a file modified before the run, or
// HEAD).
func Run(ctx context.Context, opts Options, gaps Gaps, idx Indexer, git Committer, log Logger) (Summary, error) {
	if log == nil {
		log = nopLogger{}
	}
	RecoverLeftovers(log)
	r, err := newRunner(opts, idx, git, log)
	if err != nil {
		return Summary{}, err
	}
	err = r.run(ctx, gaps)
	return r.sum, err
}

// run documents the gaps group by group (see Run).
func (r *runner) run(ctx context.Context, gaps Gaps) error {
	list, err := gaps.Next(r.opts.Repo, r.opts.MaxItems)
	if err != nil {
		return fmt.Errorf("autodoc: gaps: %w", err)
	}
	groups := groupGaps(list, r.opts.MaxItems, r.groupSize)
	for i, g := range groups {
		gctx, cancel, ok := r.groupContext(ctx)
		if !ok {
			r.skip(groups[i:])
			break
		}
		err := r.document(gctx, g)
		cancel()
		if err != nil {
			r.skip(groups[i+1:])
			return err
		}
	}
	r.log.Printf("autodoc %s: %s", r.opts.Repo, r.sum)
	return nil
}

// group is one module's gaps in rank order.
type group struct {
	module string
	gaps   []Gap
}

// keys returns the stable keys of g's gaps.
func (g group) keys() []string {
	out := make([]string, len(g.gaps))
	for i, gp := range g.gaps {
		out[i] = gp.Key
	}
	return out
}

// groupGaps groups ranked gaps by module, ordering groups by their best
// gap, keeps the first limit gaps of that order (all when limit <= 0),
// and splits each module's gaps into groups of at most size.
func groupGaps(gaps []Gap, limit, size int) []group {
	var out []group
	for _, g := range byModule(gaps, limit) {
		for len(g.gaps) > size {
			out = append(out, group{module: g.module, gaps: g.gaps[:size]})
			g.gaps = g.gaps[size:]
		}
		out = append(out, g)
	}
	return out
}

// byModule groups ranked gaps by module and keeps the first limit gaps.
func byModule(gaps []Gap, limit int) []group {
	var groups []group
	at := map[string]int{}
	for _, gp := range gaps {
		i, ok := at[gp.Module]
		if !ok {
			i = len(groups)
			at[gp.Module] = i
			groups = append(groups, group{module: gp.Module})
		}
		groups[i].gaps = append(groups[i].gaps, gp)
	}
	if limit <= 0 {
		return groups
	}
	var out []group
	for _, g := range groups {
		if limit == 0 {
			break
		}
		if len(g.gaps) > limit {
			g.gaps = g.gaps[:limit]
		}
		limit -= len(g.gaps)
		out = append(out, g)
	}
	return out
}

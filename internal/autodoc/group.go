package autodoc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/muralx/codedoc/internal/docs/write"
)

// runner carries the state of one Run.
type runner struct {
	opts      Options
	idx       Indexer
	git       Committer
	log       Logger
	bin       string
	commit    bool
	groupSize int
	maxCost   float64
	deadline  time.Time // zero without MaxMinutes
	sum       Summary
	// hold collects the documented groups of a RunHeld; nil under Run.
	hold *Held
}

// nopLogger discards lines.
type nopLogger struct{}

func (nopLogger) Printf(string, ...any) {}

// newRunner applies defaults and refuses a dirty tree unless allowed.
func newRunner(opts Options, idx Indexer, git Committer, log Logger) (*runner, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if log == nil {
		log = nopLogger{}
	}
	r := &runner{opts: opts, idx: idx, git: git, log: log, bin: claudeBin(opts.ClaudeBin), commit: opts.Commit,
		groupSize: opts.GroupSize, maxCost: opts.MaxCostUSD}
	if r.groupSize <= 0 {
		r.groupSize = DefaultGroupSize
	}
	if r.maxCost == 0 {
		r.maxCost = DefaultMaxCostUSD
	}
	if opts.MaxMinutes > 0 {
		r.deadline = opts.Now().Add(time.Duration(opts.MaxMinutes) * time.Minute)
	}
	dirty, err := git.Dirty(opts.Root)
	if err != nil {
		return nil, fmt.Errorf("autodoc: %w", err)
	}
	switch {
	case opts.AllowDirty:
		r.commit = false
	case dirty:
		return nil, ErrDirty
	}
	return r, nil
}

// minBudget is the least a claude run is started with.
const minBudget = 0.01

// groupCost is what the next claude run may spend: MaxCostUSD, or what
// is left of BudgetUSD when that is less.
func (r *runner) groupCost() float64 {
	if left := r.opts.BudgetUSD - r.sum.Cost; r.opts.BudgetUSD > 0 && (r.maxCost < 0 || left < r.maxCost) {
		return left
	}
	return r.maxCost
}

// groupContext bounds the next group by the remaining time; ok is false
// when the run must stop: it was cancelled, or its time or budget is
// used up.
func (r *runner) groupContext(ctx context.Context) (context.Context, context.CancelFunc, bool) {
	if ctx.Err() != nil {
		return ctx, func() {}, false
	}
	if r.opts.BudgetUSD > 0 && r.groupCost() < minBudget {
		r.log.Printf("autodoc %s: the run's budget of $%.2f is spent", r.opts.Repo, r.opts.BudgetUSD)
		return ctx, func() {}, false
	}
	if r.deadline.IsZero() {
		c, cancel := context.WithCancel(ctx)
		return c, cancel, true
	}
	left := r.deadline.Sub(r.opts.Now())
	if left <= 0 {
		return ctx, func() {}, false
	}
	c, cancel := context.WithTimeout(ctx, left)
	return c, cancel, true
}

// skip counts the gaps of groups that were not attempted.
func (r *runner) skip(groups []group) {
	for _, g := range groups {
		r.sum.Skipped += len(g.gaps)
	}
}

// before is the state a group starts from.
type before struct {
	tree treeState
	docs map[string]DocAt
}

// document runs claude for one group, checks the result, and commits or
// restores. It returns an error when the run must stop: a failed
// commit, or a change it cannot undo safely.
func (r *runner) document(ctx context.Context, g group) error {
	id, err := r.runID()
	if err != nil {
		return err
	}
	pre, err := r.snapshot()
	if err != nil {
		return err
	}
	m, err := markRun(id)
	if err != nil {
		return err
	}
	held := false
	defer func() {
		if !held {
			_ = m.release()
		}
	}()
	if r.opts.OnGroup != nil {
		r.opts.OnGroup(id)
	}
	budget := r.groupCost()
	env, invokeErr := invoke(ctx, invocation{bin: r.bin, root: r.opts.Root, id: id,
		prompt: Prompt(g.module, g.gaps), keys: len(g.gaps), maxCost: budget, model: r.opts.Model})
	r.count(env, budget)
	o, err := r.outcome(id, pre.tree)
	if err != nil {
		return err
	}
	if reason := r.check(o, invokeErr, env, g.keys(), pre.docs); reason != "" {
		return r.fail(g, id, m, o, reason)
	}
	if held = r.hold != nil; held {
		r.hold.add(g, id, m, o.paths)
		return nil
	}
	return r.finish(g, id, m, o)
}

// finish ends a documented group of a Run: it commits the group's files
// when asked and drops its journal.
func (r *runner) finish(g group, id string, m *marker, o outcome) error {
	if r.commit && len(o.paths) > 0 {
		if err := r.git.Commit(r.opts.Root, o.paths, commitMessage(g.module, g.keys())); err != nil {
			return errors.Join(err, r.fail(g, id, m, o, err.Error()))
		}
	}
	r.log.Printf("autodoc %s: documented %s (%d keys, run %s)", r.opts.Repo, g.module, len(g.gaps), id)
	r.sum.Documented += len(g.gaps)
	return errors.Join(m.done(), dropJournal(id))
}

// snapshot records the working tree and the docs of the repo.
func (r *runner) snapshot() (before, error) {
	tree, err := snapshotTree(r.opts.Root)
	if err != nil {
		return before{}, fmt.Errorf("autodoc: recording the working tree: %w", err)
	}
	docs, err := r.idx.Docs(r.opts.Repo)
	if err != nil {
		return before{}, fmt.Errorf("autodoc: recording docs: %w", err)
	}
	return before{tree: tree, docs: docs}, nil
}

// count adds a claude run's cost, or notes that it reported none (a
// held run also keeps the budget such a run had, see Held.Unreported).
func (r *runner) count(env Envelope, budget float64) {
	if !env.reported {
		r.sum.UnknownCost++
		if r.hold != nil {
			r.hold.Unreported += max(budget, 0)
		}
		return
	}
	r.sum.Cost += env.Cost
}

// check returns why a group failed, or "" when every key is now ok and
// nothing but those keys' docs changed.
func (r *runner) check(o outcome, invokeErr error, env Envelope, keys []string, docs map[string]DocAt) string {
	switch {
	case len(o.outside) > 0:
		return "journaled files outside the repo root: " + strings.Join(o.outside, ", ")
	case o.tree.any():
		return r.treeReason(o.tree)
	case invokeErr != nil:
		return failureWith(invokeErr, env)
	case env.IsError:
		return env.failure()
	}
	if err := r.idx.Reindex(r.opts.Repo, o.paths); err != nil {
		return "reindex: " + err.Error()
	}
	if reason := r.checkStates(keys); reason != "" {
		return reason
	}
	return r.checkDrift(o.paths, keys, docs)
}

// failureWith describes a failed claude process, with the envelope's
// subtype when it printed one.
func failureWith(err error, env Envelope) string {
	if env.Subtype == "" {
		return err.Error()
	}
	return err.Error() + " (subtype " + env.Subtype + ")"
}

// checkStates returns which keys are not ok, or "".
func (r *runner) checkStates(keys []string) string {
	states, err := r.idx.States(keys)
	if err != nil {
		return "doc check: " + err.Error()
	}
	var bad []string
	for _, k := range keys {
		if st := states[k]; st != StateOK {
			bad = append(bad, fmt.Sprintf("%s is %q", k, st))
		}
	}
	if len(bad) > 0 {
		return "doc check: " + strings.Join(bad, ", ")
	}
	return ""
}

// checkDrift returns which docs outside keys changed in the journaled
// paths, or "".
func (r *runner) checkDrift(paths, keys []string, docs map[string]DocAt) string {
	after, err := r.idx.Docs(r.opts.Repo)
	if err != nil {
		return "doc check: " + err.Error()
	}
	if drift := docDrift(docs, after, paths, keys); len(drift) > 0 {
		return "changed docs of keys outside the group: " + strings.Join(drift, ", ")
	}
	return ""
}

// docDrift lists the keys living in paths whose doc or presence differs
// between before and after, other than keys.
func docDrift(before, after map[string]DocAt, paths, keys []string) []string {
	var out []string
	check := func(k string) {
		b, inBefore := before[k]
		a, inAfter := after[k]
		touched := slices.Contains(paths, b.Path) || slices.Contains(paths, a.Path)
		if touched && (inBefore != inAfter || b.Hash != a.Hash) && !slices.Contains(keys, k) && !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	for k := range before {
		check(k)
	}
	for k := range after {
		check(k)
	}
	slices.Sort(out)
	return out
}

// fail restores the group's files from the journal, which ends the run
// cleanly (m is removed; a run whose restore failed is retried by the
// next autodoc run), and reverts what the agent changed outside it, re-indexes, and logs why the group failed.
// It returns an error when the run must stop: the agent changed files
// that were already modified before the run, or moved HEAD.
func (r *runner) fail(g group, id string, m *marker, o outcome, reason string) error {
	if r.hold != nil {
		return r.hold.fail(g, id, m, o, reason)
	}
	r.sum.Failed += len(g.gaps)
	r.log.Printf("autodoc %s: %s failed (run %s): %s", r.opts.Repo, g.module, id, reason)
	keep := len(o.outside) > 0 || len(o.tree.clean) > 0
	kept, err := write.Restore(id, keep)
	if err == nil {
		err = m.done()
	}
	if err != nil {
		r.log.Printf("autodoc %s: restoring run %s: %v", r.opts.Repo, id, err)
	}
	for _, f := range kept {
		r.log.Printf("autodoc %s: kept %s as edited during run %s (its original stays in the journal)", r.opts.Repo, f, id)
	}
	if keep {
		dir, _ := write.RunDir(id)
		r.log.Printf("autodoc %s: journal of run %s kept at %s", r.opts.Repo, id, dir)
	}
	reindex := o.paths
	if len(o.tree.clean) > 0 {
		dir, _ := write.RunDir(id)
		if err := revertClean(o.top, filepath.Join(dir, "reverted"), o.tree.clean); err != nil {
			r.log.Printf("autodoc %s: reverting run %s: %v", r.opts.Repo, id, err)
		}
		reindex = append(slices.Clone(reindex), r.relInRoot(o.tree.clean)...)
	}
	if err := r.idx.Reindex(r.opts.Repo, reindex); err != nil {
		r.log.Printf("autodoc %s: reindex after restore: %v", r.opts.Repo, err)
	}
	return o.tree.fatal()
}

// commitMessage returns the commit message of a documented group.
func commitMessage(module string, keys []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "docs(codedoc): document %s\n\n", module)
	for _, k := range keys {
		b.WriteString(k + "\n")
	}
	return b.String()
}

// runID returns the journal run id of the next group.
func (r *runner) runID() (string, error) {
	if r.hold != nil {
		return r.hold.nextID(r.opts.Now())
	}
	return newRunID(r.opts.Now())
}

// treeReason says what changed in the working tree outside the journal:
// under Run the agent is held to have done it, under RunHeld someone
// else.
func (r *runner) treeReason(c treeChanges) string {
	if r.hold != nil {
		return "the working tree changed during the run: " + c.describe()
	}
	return "the agent wrote outside codedoc doc set: " + c.describe()
}

// newRunID returns a unique journal run id.
func newRunID(now time.Time) (string, error) {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return "autodoc-" + now.UTC().Format("20060102T150405") + "-" + hex.EncodeToString(buf[:]), nil
}

// dropJournal deletes the journal of a successful run.
func dropJournal(id string) error {
	dir, err := write.RunDir(id)
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

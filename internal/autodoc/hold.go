package autodoc

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/muralx/codedoc/internal/docs/write"
	"github.com/muralx/codedoc/internal/gitmeta"
)

// errInterrupted stops a held run whose working tree changed under it.
var errInterrupted = errors.New("autodoc: the working tree changed during the run")

// Held is a finished RunHeld whose writes wait for one decision: Commit
// or Keep them all, or Revert them all. Until then the journal of every
// documented group stays, with its marker locked, so a process that
// dies first has the whole run restored by RecoverLeftovers.
type Held struct {
	// Summary counts the run's gaps by outcome and its cost.
	Summary Summary
	// Keys are the documented keys, in the order they were written.
	Keys []string
	// Paths are the repo-relative files the documented groups wrote,
	// sorted, each once.
	Paths []string
	// Failed are the keys of the groups that failed on their own: their
	// docs did not come out ok, or claude ended with an error.
	Failed []string
	// Unreported sums what the claude runs that reported no cost were
	// allowed to spend: they timed out, were cancelled or crashed, and
	// may have spent up to that. Summary.Cost leaves them out.
	Unreported float64
	// Interrupted says what changed in the working tree outside the
	// run's journal while it ran; "" when nothing did. The run stopped
	// there, and Revert is the only sound way to finish it.
	Interrupted string

	r      *runner
	base   string // the journal run id every group's id starts with
	seq    int    // groups started
	groups []heldGroup
}

// heldGroup is one documented group: its journal and locked marker.
type heldGroup struct {
	id string
	m  *marker
}

// RunHeld is Run for a caller that takes the run as one unit, as
// documentation upkeep does: no group is committed, and the journals of
// the documented groups are kept until the caller commits, keeps or
// reverts the run as a whole (see Held). A group that fails on its own
// is restored at once, as under Run. Anything that changed in the
// working tree outside the run's journal, a moved HEAD included, is
// taken for someone else's work, not the agent's: it is left exactly as
// it is, the group in progress is restored, and the run stops with
// Held.Interrupted set. Options.Commit is ignored. On an error
// everything the run wrote is reverted; the Held returned with it, nil
// when the run could not start, only tells what was spent.
func RunHeld(ctx context.Context, opts Options, gaps Gaps, idx Indexer, git Committer, log Logger) (*Held, error) {
	if log == nil {
		log = nopLogger{}
	}
	RecoverLeftovers(log)
	r, err := newRunner(opts, idx, git, log)
	if err != nil {
		return nil, err
	}
	h := &Held{r: r}
	r.hold = h
	err = r.run(ctx, gaps)
	h.Summary = r.sum
	if err != nil && !errors.Is(err, errInterrupted) {
		return h, errors.Join(err, h.Revert())
	}
	return h, nil
}

// nextID returns the journal run id of the next group: the ids of one
// held run share a base and count up, so they sort in the order written.
func (h *Held) nextID(now time.Time) (string, error) {
	if h.base == "" {
		base, err := newRunID(now)
		if err != nil {
			return "", err
		}
		h.base = base
	}
	h.seq++
	return fmt.Sprintf("%s-%03d", h.base, h.seq), nil
}

// add keeps a documented group for the caller's decision.
func (h *Held) add(g group, id string, m *marker, paths []string) {
	r := h.r
	r.log.Printf("autodoc %s: documented %s (%d keys, run %s)", r.opts.Repo, g.module, len(g.gaps), id)
	r.sum.Documented += len(g.gaps)
	h.groups = append(h.groups, heldGroup{id: id, m: m})
	h.Keys = append(h.Keys, g.keys()...)
	for _, p := range paths {
		if !slices.Contains(h.Paths, p) {
			h.Paths = append(h.Paths, p)
		}
	}
	slices.Sort(h.Paths)
}

// fail restores a group that failed, leaving the earlier groups as they
// are, and re-indexes its files. When the reason is a change outside
// the journal, that change is left alone and the run stops.
func (h *Held) fail(g group, id string, m *marker, o outcome, reason string) error {
	r := h.r
	r.sum.Failed += len(g.gaps)
	r.log.Printf("autodoc %s: %s failed (run %s): %s", r.opts.Repo, g.module, id, reason)
	h.restore(id, m, len(o.outside) > 0)
	if err := r.idx.Reindex(r.opts.Repo, o.paths); err != nil {
		r.log.Printf("autodoc %s: reindex after restore: %v", r.opts.Repo, err)
	}
	if o.tree.any() {
		h.Interrupted = reason
		return errInterrupted
	}
	h.Failed = append(h.Failed, g.keys()...)
	return nil
}

// restore puts the files of one group back from its journal and ends
// it, logging what it could not do and the files it kept.
func (h *Held) restore(id string, m *marker, keep bool) {
	r := h.r
	kept, err := write.RestoreUnless(id, keep, h.settled())
	if err == nil {
		err = m.done()
	}
	if err != nil {
		r.log.Printf("autodoc %s: restoring run %s: %v", r.opts.Repo, id, err)
	}
	for _, f := range kept {
		r.log.Printf("autodoc %s: kept %s as edited during run %s (its original stays in the journal)", r.opts.Repo, f, id)
	}
}

// settled tells the journaled files a restore leaves alone: those of
// the working tree that git reports unchanged, because someone committed
// them while the run was going (putting the original back would leave
// an uncommitted change nobody made). It is nil outside git.
func (h *Held) settled() func(abs string) bool {
	top, err := gitmeta.Output(h.r.opts.Root, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil
	}
	paths, err := dirtyPaths(top)
	if err != nil {
		return nil
	}
	return func(abs string) bool {
		abs = canonical(abs)
		return strings.HasPrefix(abs, top+string(filepath.Separator)) && !slices.Contains(paths, abs)
	}
}

// Commit commits every file the run wrote as one commit with message
// msg, through the run's Committer, then ends the run (see Keep). A run
// that wrote nothing commits nothing. On an error nothing is ended:
// Revert still works.
func (h *Held) Commit(msg string) error {
	if err := h.r.git.Commit(h.r.opts.Root, h.Paths, msg); err != nil {
		return err
	}
	return h.Keep()
}

// Keep ends the run leaving its writes in the working tree: the
// journals are dropped and the markers removed.
func (h *Held) Keep() error {
	var errs []error
	for _, g := range h.groups {
		errs = append(errs, g.m.done(), g.m.release(), dropJournal(g.id))
	}
	h.groups = nil
	return errors.Join(errs...)
}

// Revert puts back, newest group first, every file the run wrote, so
// the working tree is as it was before the run, and re-indexes those
// files. A file edited since the run wrote it is kept and logged, and
// so is one committed meanwhile (see settled).
func (h *Held) Revert() error {
	for _, g := range slices.Backward(h.groups) {
		h.restore(g.id, g.m, false)
		_ = g.m.release()
	}
	h.groups = nil
	return h.r.idx.Reindex(h.r.opts.Repo, h.Paths)
}

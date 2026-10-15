package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/muralx/codedoc/internal/daemon"
	"github.com/muralx/codedoc/internal/docs/write"
	"github.com/muralx/codedoc/internal/upkeep"
)

// upkeepState is a repo's documentation upkeep as the daemon tracks it
// (efficiency spec section 7). repo.mu guards it, except skip and
// failedAt, which only the upkeep job touches: jobs of a repo run one at
// a time.
type upkeepState struct {
	// lastEvent is when the repo last saw a change that was not a
	// running upkeep's own write, or when the daemon started watching,
	// or when the last run or failed check ended: the quiet time counts
	// from it.
	lastEvent time.Time
	// run is the run in progress, nil when none.
	run *upkeepRun
	// status holds what the last check and run found.
	status daemon.UpkeepStatus
	// skip is upkeep.Env.Skip, kept between runs.
	skip map[string]bool
	// failedAt is the HEAD commit at which a run ended with an error
	// (not an interruption): no other run starts at that commit, so a
	// failure that repeats, such as a commit hook that refuses the docs
	// commit, is paid for once.
	failedAt string
}

// upkeepRun is a run in progress.
type upkeepRun struct {
	cancel context.CancelFunc
	// ids are the journal run ids of the groups started so far.
	ids []string
	// why says which change stopped the run; "" while none did.
	why string
}

// initUpkeep starts the quiet time at now and loads when upkeep last
// ran on the repo.
func (r *repo) initUpkeep(now time.Time) {
	var last time.Time
	if row, err := r.sp.store.RepoByPath(r.cfg.Path); err == nil {
		if at, err := r.sp.store.UpkeepLastRun(row.ID); err == nil && at > 0 {
			last = time.Unix(at, 0).UTC()
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.up.lastEvent, r.up.skip = now, map[string]bool{}
	r.up.status = daemon.UpkeepStatus{Enabled: r.sp.ws.Upkeep.Enabled, LastRun: last}
}

// noteChange counts a batch of changes against the quiet time. While
// an upkeep run is in progress, a batch holding only files that run
// wrote is the run's own and is ignored; any other batch (another file,
// a moved HEAD, a rescan) means the repo stopped being quiet or clean,
// and the run is cancelled, which reverts it (see upkeep.Run).
func (r *repo) noteChange(c daemon.Change) {
	now := r.sp.svc.opts.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	if run := r.up.run; run != nil {
		foreign := run.foreign(r.cfg.Path, c)
		if foreign == "" {
			return
		}
		if run.why == "" {
			run.why = foreign
			run.cancel()
		}
	}
	r.up.lastEvent = now
}

// foreign says what in a batch of changes the run did not do itself;
// "" when the batch holds only files of the run's journals.
func (u *upkeepRun) foreign(root string, c daemon.Change) string {
	switch {
	case c.HeadMoved:
		return "HEAD moved during the run"
	case c.Rescan:
		return "the repo was rescanned during the run"
	}
	own := map[string]bool{}
	for _, id := range u.ids {
		files, _ := write.Files(id)
		for _, f := range files {
			own[f] = true
		}
	}
	roots := []string{root}
	if resolved, err := filepath.EvalSymlinks(root); err == nil && resolved != root {
		roots = append(roots, resolved)
	}
	for _, p := range c.Paths {
		if !ownPath(own, roots, p) {
			return p + " changed during the run"
		}
	}
	if len(c.Paths) == 0 {
		return "a manifest changed during the run"
	}
	return ""
}

// ownPath reports whether the repo-relative path p is a journaled file
// or the temporary file a doc write renames into one
// (".<name>.codedoc-*", see write).
func ownPath(own map[string]bool, roots []string, p string) bool {
	dir, base := filepath.Split(filepath.FromSlash(p))
	if name, ok := strings.CutPrefix(base, "."); ok {
		if i := strings.LastIndex(name, ".codedoc-"); i > 0 {
			base = name[:i]
		}
	}
	for _, root := range roots {
		if own[filepath.Join(root, dir, base)] {
			return true
		}
	}
	return false
}

// upkeepDue reports whether an upkeep check is due at now: the
// workspace opted in, the repo's directory is there, no run is in
// progress, and the repo saw no change for the quiet time.
func (r *repo) upkeepDue(now time.Time) bool {
	u := r.sp.ws.Upkeep
	if !u.Enabled || r.sp.gen.watcher.Missing(r.sp.ws.Name, r.cfg.Name) {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.up.run == nil && now.Sub(r.up.lastEvent) >= u.Quiet.D()
}

// runUpkeep checks the repo and, when it is ready, documents what its
// branch changed (see upkeep.Evaluate and upkeep.Run). A session lock
// does not hold it back: a clean and quiet tree is the condition. A
// failure is kept as the repo's upkeep error, not returned, and the
// next check waits out the quiet time again; after a run that ended
// with an error no other run starts until HEAD is another commit.
func (r *repo) runUpkeep(ctx context.Context) (err error) {
	st, err := r.jobStore()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, st.Close()) }()
	o := r.sp.svc.opts
	r.mu.Lock()
	last := r.up.lastEvent
	r.mu.Unlock()
	run := &upkeepRun{}
	env := upkeep.Env{Store: st, Workspace: r.sp.ws, Repo: r.cfg, Embedder: r.sp.svc.embedder, Now: o.Now,
		LastEvent: last, Skip: r.up.skip, Log: r.sp.svc.log, ClaudeBin: o.ClaudeBin, MaxMinutes: o.AutodocMinutes,
		OnGroup: func(id string) {
			r.mu.Lock()
			run.ids = append(run.ids, id)
			r.mu.Unlock()
		}}
	c, err := upkeep.Evaluate(env)
	if r.noteCheck(c, err); err != nil || !c.Ready() {
		return nil
	}
	if c.Head == r.up.failedAt {
		r.rest()
		return nil
	}
	ctx, run.cancel = context.WithCancel(ctx)
	defer run.cancel()
	r.setRun(run)
	r.sp.svc.log.Printf("%s: upkeep: %d symbol(s) to document on %s", r.label(), len(c.Pending), c.Branch)
	res, err := upkeep.Run(ctx, env, c)
	r.noteRun(c, res, err)
	return nil
}

// rest pushes the next check a quiet time away.
func (r *repo) rest() {
	now := r.sp.svc.opts.Now()
	r.mu.Lock()
	r.up.lastEvent = now
	r.mu.Unlock()
}

// setRun marks run as the run in progress.
func (r *repo) setRun(run *upkeepRun) {
	r.mu.Lock()
	r.up.run = run
	r.mu.Unlock()
}

// noteCheck keeps what a check found; a failed one is logged and pushes
// the next check a quiet time away.
func (r *repo) noteCheck(c upkeep.Check, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := &r.up.status
	s.SpentToday, s.CapReached = c.SpentToday, c.Wait == upkeep.WaitCap
	if err != nil {
		r.sp.svc.log.Printf("%s: upkeep: %v", r.label(), err)
		s.LastError, r.up.lastEvent = err.Error(), r.sp.svc.opts.Now()
		return
	}
	s.Pending, s.Waiting = len(c.Pending), c.Wait
}

// noteRun ends the run in progress and keeps and logs its outcome. The
// next check waits out the quiet time again.
func (r *repo) noteRun(c upkeep.Check, res upkeep.Result, err error) {
	now := r.sp.svc.opts.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	run := r.up.run
	r.up.run, r.up.lastEvent = nil, now
	s := &r.up.status
	s.SpentToday, s.Pending = res.SpentToday, res.Left
	switch {
	case err != nil:
		r.up.failedAt, s.Pending = c.Head, len(c.Pending)
		s.LastError = firstLine(err.Error()) + "; no other run before the next commit"
	case res.Interrupted != "":
		if run.why != "" {
			res.Interrupted = run.why
		}
		s.Pending = len(c.Pending)
		s.LastError = "run undone: " + res.Interrupted + "; it will be retried"
	default:
		s.LastError, s.LastRun = undocumented(res), time.Unix(now.Unix(), 0).UTC()
		r.sp.svc.log.Printf("%s: upkeep: %s on %s%s, %d left", r.label(), res.Summary, c.Branch, commitNote(res.Commit), res.Left)
		return
	}
	r.sp.svc.log.Printf("%s: upkeep: %s", r.label(), s.LastError)
}

// undocumented is the upkeep error of a finished run that documented
// nothing although it tried; "" for any other run.
func undocumented(res upkeep.Result) string {
	if res.Summary.Documented > 0 || res.Summary.Failed == 0 {
		return ""
	}
	return fmt.Sprintf("the last run documented nothing: %d failed (the daemon log says why)", res.Summary.Failed)
}

// firstLine returns s up to its first line break.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// commitNote names the docs commit of a run in a log line.
func commitNote(commit string) string {
	if commit == "" {
		return ""
	}
	return fmt.Sprintf(" (commit %.7s)", commit)
}

// upkeepStatus is the repo's upkeep status at now. While the repo is
// not quiet yet, that is what holds upkeep back.
func (r *repo) upkeepStatus(now time.Time) daemon.UpkeepStatus {
	s := r.up.status
	s.Running = r.up.run != nil
	switch {
	case !s.Enabled:
		s.Waiting = ""
	case s.Running:
		s.Waiting = ""
	case !s.CapReached && now.Sub(r.up.lastEvent) < r.sp.ws.Upkeep.Quiet.D():
		s.Waiting = upkeep.WaitQuiet
	}
	return s
}

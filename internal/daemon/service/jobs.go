package service

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/muralx/codedoc/internal/autodoc"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/query"
	"github.com/muralx/codedoc/internal/scip"
	"github.com/muralx/codedoc/internal/session"
	"github.com/muralx/codedoc/internal/store"
)

// schedule starts due SCIP and autodoc runs and due upkeep checks
// every Tick until ctx ends.
func (g *generation) schedule(ctx context.Context) {
	now := g.svc.opts.Now()
	for _, sp := range g.spaces {
		for _, r := range sp.repos {
			r.initSchedules(now)
			r.initUpkeep(now)
		}
	}
	t := time.NewTicker(g.svc.opts.Tick)
	defer t.Stop()
	for {
		g.startDue(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// scipSchedule and docSchedule are the repo's schedules.
func (r *repo) scipSchedule() Schedule {
	if !slices.Contains(r.sp.ws.SCIP.Enabled, r.cfg.Name) {
		return Schedule{}
	}
	return Schedule{Every: r.sp.ws.SCIP.Every.D()}
}

func (r *repo) docSchedule() Schedule {
	a := r.sp.ws.Autodoc
	return Schedule{Every: a.Every.D(), At: a.At}
}

// initSchedules sets the first runs: SCIP at once for a repo never
// merged, else one interval from now; autodoc per its schedule.
func (r *repo) initSchedules(now time.Time) {
	next := r.scipSchedule().First(now)
	if row, err := r.sp.store.RepoByPath(r.cfg.Path); r.scipSchedule().Enabled() && (err != nil || row.SCIPCommit == "") {
		next = now
	}
	r.mu.Lock()
	r.nextSCIP, r.nextDoc = next, r.docSchedule().First(now)
	r.mu.Unlock()
}

// startDue starts every due job of a caught-up repo whose previous job
// finished, and moves its next run on. An upkeep check is due on every
// tick of a quiet repo (see upkeepDue).
func (g *generation) startDue(ctx context.Context) {
	now := g.svc.opts.Now()
	for _, sp := range g.spaces {
		for _, r := range sp.repos {
			if ctx.Err() != nil || !r.isReady() || !r.jobs.TryLock() {
				continue
			}
			scipDue, docDue := r.due(now)
			upDue := r.upkeepDue(now)
			if !scipDue && !docDue && !upDue {
				r.jobs.Unlock()
				continue
			}
			g.jobs.Go(func() {
				defer r.jobs.Unlock()
				r.runJob(ctx, scipDue, "scip", r.runSCIP)
				r.runJob(ctx, docDue, "autodoc", r.runAutodoc)
				r.runJob(ctx, upDue, "upkeep", r.runUpkeep)
			})
		}
	}
}

// runJob runs a due job safely (see safely), unless ctx ended.
func (r *repo) runJob(ctx context.Context, due bool, what string, job func(context.Context) error) {
	if due && ctx.Err() == nil {
		_ = r.safely(what, func() error { return job(ctx) })
	}
}

// due reports which jobs are due at now and schedules their next runs.
func (r *repo) due(now time.Time) (scipDue, docDue bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if scipDue = !r.nextSCIP.IsZero() && !now.Before(r.nextSCIP); scipDue {
		r.nextSCIP = r.scipSchedule().After(r.nextSCIP, now)
	}
	if docDue = !r.nextDoc.IsZero() && !now.Before(r.nextDoc); docDue {
		r.nextDoc = r.docSchedule().After(r.nextDoc, now)
	}
	return scipDue, docDue
}

func (r *repo) isReady() bool {
	select {
	case <-r.ready:
		return true
	default:
		return false
	}
}

// jobStore opens a separate handle on the workspace database, so a long
// job does not hold the connection the watcher's indexing uses.
func (r *repo) jobStore() (*store.Store, error) { return store.Open(r.sp.dbPath()) }

// runSCIP brings the repo's index up to date, runs its SCIP indexers and
// merges their output. A missing indexer is logged once per daemon.
func (r *repo) runSCIP(ctx context.Context) (err error) {
	st, err := r.jobStore()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, st.Close()) }()
	if _, err := index.EnsureContext(ctx, st, r.cfg, r.sp.options()); err != nil {
		return err
	}
	row, err := st.RepoByPath(r.cfg.Path)
	if err != nil {
		return err
	}
	outcomes, err := scip.Runner{}.RunRepo(ctx, st, row)
	if err != nil {
		return err
	}
	var failed []string
	for _, o := range outcomes {
		if o.Missing != nil && !r.warnOnce(o.Missing.Bin) {
			continue
		}
		r.sp.svc.log.Printf("%s: scip %s: %s", r.label(), o.Lang, o.Text)
		if o.Failed {
			failed = append(failed, o.Lang+": "+o.Text)
		}
	}
	if len(failed) > 0 {
		return errors.New(failed[0])
	}
	return nil
}

// warnOnce reports whether bin was not reported missing before.
func (r *repo) warnOnce(bin string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.warned[bin] {
		return false
	}
	if r.warned == nil {
		r.warned = map[string]bool{}
	}
	r.warned[bin] = true
	return true
}

// runAutodoc documents the repo's top gaps unless a session holds its
// lock or its working tree is dirty; both skips are logged, not errors.
func (r *repo) runAutodoc(ctx context.Context) (err error) {
	held, err := session.Held(r.sp.dataDir(), r.sp.ws.Name, r.cfg.Name)
	if err != nil {
		return err
	}
	if held {
		r.sp.svc.log.Printf("%s: autodoc skipped: a session holds the repo's lock", r.label())
		return nil
	}
	st, err := r.jobStore()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, st.Close()) }()
	x := autodoc.Indexed{Ctx: &query.Ctx{Store: st, Workspace: r.sp.ws, Embedder: r.sp.svc.embedder}}
	a := r.sp.ws.Autodoc
	opts := autodoc.Options{Repo: r.cfg.Name, Root: r.cfg.Path, MaxItems: a.MaxItems,
		MaxMinutes: r.sp.svc.opts.AutodocMinutes, Commit: a.Commit, GroupSize: a.GroupSize, MaxCostUSD: a.MaxCostUSD, Model: a.Model,
		ClaudeBin: r.sp.svc.opts.ClaudeBin, Now: r.sp.svc.opts.Now}
	_, err = autodoc.Run(ctx, opts, x, x, autodoc.Git{}, r.sp.svc.log)
	if errors.Is(err, autodoc.ErrDirty) {
		r.sp.svc.log.Printf("%s: autodoc skipped: the working tree has uncommitted changes", r.label())
		return nil
	}
	return err
}

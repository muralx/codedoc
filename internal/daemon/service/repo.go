package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/daemon"
	"github.com/muralx/codedoc/internal/deps"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/store"
)

// space is one served workspace and its database.
type space struct {
	svc   *Service
	gen   *generation
	ws    *config.Workspace
	store *store.Store
	// dbID identifies the database file store has open.
	dbID  daemon.FileID
	repos []*repo
}

// openSpace opens the workspace database.
func openSpace(g *generation, ws *config.Workspace) (*space, error) {
	st, err := store.Open(g.cfg.DBPath(ws.Name))
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", ws.Name, err)
	}
	id, err := daemon.StatFileID(g.cfg.DBPath(ws.Name))
	if err != nil {
		return nil, errors.Join(fmt.Errorf("open %s: %w", ws.Name, err), st.Close())
	}
	sp := &space{svc: g.svc, gen: g, ws: ws, store: st, dbID: id}
	for _, r := range ws.Repos {
		sp.repos = append(sp.repos, &repo{sp: sp, cfg: r, ready: make(chan struct{})})
	}
	return sp, nil
}

// dbPath is the workspace database.
func (sp *space) dbPath() string { return sp.gen.cfg.DBPath(sp.ws.Name) }

// dataDir holds the workspace database and session locks.
func (sp *space) dataDir() string { return filepath.Dir(sp.dbPath()) }

// options are the index options of the workspace, without a file limit.
func (sp *space) options() index.Options {
	return index.Options{Workspace: sp.ws.Name, Exclude: sp.ws.Exclude, MaxFiles: math.MaxInt,
		RepoOrder: sp.ws.RepoNames(), Embedder: sp.svc.embedder}
}

// catchUp brings every repo of the workspace up to date with its working
// tree, logging its progress, then embeds the vectors still missing,
// which loads the model once for the daemon's lifetime, and indexes the
// dependencies deps.db lacks (see syncDeps). Each repo is marked ready,
// failed or not, so its changes are handled from then on.
func (sp *space) catchUp(ctx context.Context) {
	sp.svc.log.Printf("catch-up %s: %d repo(s)", sp.ws.Name, len(sp.repos))
	for i, r := range sp.repos {
		if ctx.Err() != nil {
			return
		}
		start := time.Now()
		var f index.Freshness
		err := r.safely("index", func() (err error) {
			f, err = index.EnsureContext(ctx, sp.store, r.cfg, sp.options())
			return err
		})
		if ctx.Err() != nil {
			return
		}
		r.record(err)
		sp.svc.log.Printf("catch-up %s (%d/%d): %s in %s", r.label(), i+1, len(sp.repos), catchUpNote(f, err),
			time.Since(start).Round(time.Millisecond))
		close(r.ready)
	}
	if sp.svc.embedder != nil {
		sp.eachRepo(ctx, "embed", func(r *repo) error { return r.embed() })
	}
	sp.eachRepo(ctx, "deps", func(r *repo) error { return r.syncDeps(ctx, true) })
}

// eachRepo runs fn on every repo of the workspace, safely (see
// repo.safely), until ctx ends.
func (sp *space) eachRepo(ctx context.Context, what string, fn func(*repo) error) {
	for _, r := range sp.repos {
		if ctx.Err() != nil {
			return
		}
		_ = r.safely(what, func() error { return fn(r) })
	}
}

// syncDeps discovers the repo's dependencies where a manifest, a lock
// file or an install marker changed (always when full) and indexes the
// package versions deps.db lacks, those a query left pending included
// (deps spec section 9); with an embedder it then embeds their vectors.
func (r *repo) syncDeps(ctx context.Context, full bool) error {
	start := time.Now()
	sum, err := deps.Sync(ctx, r.sp.store, r.sp.ws, []*config.Repo{r.cfg},
		deps.Options{DB: r.sp.gen.cfg.DepsDBPath(), Full: full})
	for _, p := range sum.Problems {
		r.sp.svc.log.Printf("%s: deps: unreadable manifest %s", r.label(), p)
	}
	if sum.Indexed+sum.Failed > 0 {
		r.sp.svc.log.Printf("%s: deps: indexed %d package version(s), %d failed in %s", r.label(), sum.Indexed, sum.Failed,
			time.Since(start).Round(time.Millisecond))
	}
	if err != nil || r.sp.svc.embedder == nil {
		return err
	}
	return r.embedDeps()
}

// embedDeps embeds the vectors the package versions the repo uses still
// lack in deps.db (deps spec section 8).
func (r *repo) embedDeps() error {
	start := time.Now()
	res, err := deps.EmbedVectors(r.sp.store, r.sp.gen.cfg.DepsDBPath(), r.sp.svc.embedder, []*config.Repo{r.cfg}, 0)
	if res.Symbols+res.Modules > 0 {
		r.sp.svc.log.Printf("%s: deps: embedded %d symbol(s), %d module(s) in %s", r.label(), res.Symbols, res.Modules,
			time.Since(start).Round(time.Millisecond))
	}
	return err
}

// repo is one served repo: its status and the work done on it.
type repo struct {
	sp    *space
	cfg   *config.Repo
	ready chan struct{} // closed after the first catch-up
	jobs  sync.Mutex    // serializes scheduled jobs

	mu        sync.Mutex
	branch    string
	commit    string
	indexedAt time.Time
	indexErr  string // the last indexing error, "" after a success
	lastErr   string // the last error of any kind
	nextSCIP  time.Time
	nextDoc   time.Time
	warned    map[string]bool // SCIP indexers reported missing
	up        upkeepState
}

func (r *repo) label() string { return r.sp.ws.Name + "/" + r.cfg.Name }

// catchUpNote describes the outcome of a catch-up.
func catchUpNote(f index.Freshness, err error) string {
	switch {
	case err != nil:
		return "failed"
	case f.Missing:
		return "directory missing, index kept"
	}
	return fmt.Sprintf("up to date, %d file(s) changed", f.Changed)
}

// apply indexes one batch of changes once the repo caught up: a HEAD
// move or a rescan runs a full freshness pass, anything else exactly the
// paths. A rescan that succeeds clears the repo's degraded state. Work
// cut short by the end of ctx is not an error: the next pass finishes it.
// The repo's dependencies are synced after every batch (see syncDeps):
// a changed source file may import a package for the first time, or be
// the last to stop importing one.
func (r *repo) apply(ctx context.Context, c daemon.Change) {
	select {
	case <-r.ready:
	case <-ctx.Done():
		return
	}
	start := time.Now()
	err := r.safely("index", func() error {
		if c.HeadMoved || c.Rescan {
			_, err := index.EnsureContext(ctx, r.sp.store, r.cfg, r.sp.options())
			return err
		}
		return index.Paths(ctx, r.sp.store, r.cfg, r.sp.options(), c.Paths)
	})
	if ctx.Err() != nil {
		return
	}
	r.record(err)
	if err != nil {
		return
	}
	if c.Rescan {
		r.sp.gen.watcher.Recovered(c)
	}
	r.sp.svc.log.Printf("%s: indexed %d path(s)%s in %s", r.label(), len(c.Paths), headNote(c),
		time.Since(start).Round(time.Millisecond))
	_ = r.safely("deps", func() error { return r.syncDeps(ctx, false) })
}

func headNote(c daemon.Change) string {
	switch {
	case c.Rescan:
		return " after a rescan"
	case c.HeadMoved:
		return " after a HEAD move"
	}
	return ""
}

// embed brings the repo's vectors up to date.
func (r *repo) embed() error {
	row, err := r.sp.store.RepoByPath(r.cfg.Path)
	if err != nil {
		return err
	}
	res, err := index.EmbedVectors(r.sp.store, r.sp.svc.embedder, store.VecScope{RepoID: row.ID}, 0)
	if err == nil && res.Symbols+res.Modules > 0 {
		r.sp.svc.log.Printf("%s: embedded %d symbol(s) and %d module(s)", r.label(), res.Symbols, res.Modules)
	}
	return err
}

// safely runs fn, turning a panic into an error, and logs a failure
// against the repo as its last error; a success clears a last error of
// the same kind.
func (r *repo) safely(what string, fn func() error) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("panic: %v\n%s", v, debug.Stack())
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		switch {
		case err != nil:
			r.sp.svc.log.Printf("%s: %s: %v", r.label(), what, err)
			r.lastErr = what + ": " + err.Error()
		case strings.HasPrefix(r.lastErr, what+": "):
			r.lastErr = ""
		}
	}()
	return fn()
}

// record notes the outcome of indexing and refreshes the cached head.
func (r *repo) record(err error) {
	row, rowErr := r.sp.store.RepoByPath(r.cfg.Path)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.indexErr = ""
	if err != nil {
		r.indexErr = err.Error()
	}
	if rowErr == nil {
		r.branch, r.commit = row.Branch, row.Commit
		r.indexedAt = time.Time{}
		if row.IndexedAt > 0 {
			r.indexedAt = time.Unix(row.IndexedAt, 0).UTC()
		}
	} else if !errors.Is(rowErr, store.ErrNotFound) {
		r.sp.svc.log.Printf("%s: reading repo row: %v", r.label(), rowErr)
	}
}

// current reports whether the repo caught up and its last indexing
// succeeded.
func (r *repo) current() bool {
	select {
	case <-r.ready:
	default:
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.indexErr == ""
}

// freshReply is the repo's answer to the fresh op.
func (r *repo) freshReply() daemon.FreshReply {
	w, ws := r.sp.gen.watcher, r.sp.ws.Name
	reply := daemon.FreshReply{Pending: w.Pending(ws, r.cfg.Name)}
	switch d := w.Degraded(ws, r.cfg.Name); {
	case w.Missing(ws, r.cfg.Name):
		reply.Note = "missing: " + r.cfg.Path + " is gone; its index is kept"
	case d != "":
		reply.Note = "degraded: " + d
	default:
		reply.Fresh = reply.Pending == 0 && r.current()
	}
	return reply
}

// status describes the repo, its queue and its freshness.
func (r *repo) status() daemon.RepoStatus {
	w, ws := r.sp.gen.watcher, r.sp.ws.Name
	fresh := r.freshReply()
	r.mu.Lock()
	defer r.mu.Unlock()
	return daemon.RepoStatus{Name: r.cfg.Name, Branch: r.branch, Commit: r.commit, IndexedAt: r.indexedAt,
		Queue: fresh.Pending, LastError: r.lastErr, NextSCIP: r.nextSCIP, NextAutodoc: r.nextDoc,
		Upkeep: r.upkeepStatus(r.sp.svc.opts.Now()), Fresh: fresh.Fresh, Degraded: w.Degraded(ws, r.cfg.Name), Missing: w.Missing(ws, r.cfg.Name)}
}

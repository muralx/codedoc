package service

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/muralx/codedoc/internal/buildinfo"
	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/daemon"
	"github.com/muralx/codedoc/internal/deps"
	"github.com/muralx/codedoc/internal/index"
)

// generation is the daemon's work under one configuration: the open
// workspace databases, the watcher of their repos and the jobs started
// on their schedules. A configuration change closes it and opens the
// next one.
type generation struct {
	svc     *Service
	cfg     *config.Config
	spaces  []*space
	watcher *daemon.Watcher
	jobs    sync.WaitGroup
}

// open opens the database of every selected workspace of cfg and starts
// watching its repos; on error everything opened is closed.
func (s *Service) open(cfg *config.Config) (*generation, error) {
	names, err := workspaceNames(cfg, s.opts.Workspace)
	if err != nil {
		return nil, err
	}
	g := &generation{svc: s, cfg: cfg}
	var specs []daemon.RepoSpec
	for _, name := range names {
		sp, err := openSpace(g, cfg.Workspaces[name])
		if err != nil {
			g.close()
			return nil, err
		}
		g.spaces = append(g.spaces, sp)
		for _, r := range sp.repos {
			specs = append(specs, daemon.RepoSpec{Workspace: name, Name: r.cfg.Name, Root: r.cfg.Path, Exclude: sp.ws.Exclude})
		}
	}
	g.watcher, err = daemon.NewWatcher(specs, daemon.WatchOptions{Debounce: s.opts.Debounce,
		HeadInterval: s.opts.HeadInterval, Log: s.log, Manifest: deps.IsTrigger})
	if err != nil {
		g.close()
		return nil, err
	}
	return g, nil
}

// generations runs one generation after the other until ctx ends: the
// first from the loaded configuration, the next each time the
// configuration file changes. A changed configuration that cannot be
// opened is logged and the previous one opened again.
func (s *Service) generations(ctx context.Context) error {
	reloads := make(chan *config.Config)
	if s.opts.ConfigPath != "" {
		go s.watchConfig(ctx, reloads)
	}
	cfg, prev := s.opts.Config, (*config.Config)(nil)
	for {
		g, err := s.open(cfg)
		if err != nil && prev != nil {
			s.log.Printf("daemon: cannot open the changed configuration, keeping the previous one: %v", err)
			cfg, prev = prev, nil
			continue
		}
		if err != nil {
			return err
		}
		next, err := s.serveGeneration(ctx, g, reloads)
		if next == nil || err != nil {
			return err
		}
		s.log.Printf("daemon: configuration changed; reopening the workspaces")
		cfg, prev = next, cfg
	}
}

// serveGeneration runs g until ctx ends (next nil), the watcher fails
// (err), or a new configuration arrives (next), then waits for g to stop.
func (s *Service) serveGeneration(ctx context.Context, g *generation, reloads <-chan *config.Config) (next *config.Config, err error) {
	gctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	s.setCurrent(g)
	go func() { done <- g.run(gctx) }()
	select {
	case err = <-done:
	case <-ctx.Done():
		cancel()
		err = <-done
	case next = <-reloads:
		cancel()
		err = <-done
	}
	s.setCurrent(nil)
	return next, err
}

// run brings every repo up to date, then indexes changes and runs due
// schedules until ctx ends or the watcher fails; it then lets work in
// progress finish, closes the databases and returns the watcher's error.
func (g *generation) run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	var watchErr error
	wg.Go(func() {
		watchErr = g.watcher.Run(ctx, g.deliver)
		cancel()
	})
	for _, sp := range g.spaces {
		wg.Go(func() { sp.catchUp(ctx) })
	}
	wg.Go(func() { g.schedule(ctx) })
	g.svc.log.Printf("daemon: serving %d workspace(s)", len(g.spaces))
	<-ctx.Done()
	wg.Wait()
	g.jobs.Wait()
	g.close()
	return watchErr
}

// close releases the databases.
func (g *generation) close() {
	for _, sp := range g.spaces {
		_ = sp.store.Close()
	}
}

// deliver indexes one batch of changes of a repo.
func (g *generation) deliver(ctx context.Context, c daemon.Change) {
	if r := g.repo(c.Workspace, c.Repo); r != nil {
		r.noteChange(c)
		r.apply(ctx, c)
	}
}

// repo returns a served repo, or nil.
func (g *generation) repo(workspace, name string) *repo {
	for _, sp := range g.spaces {
		if sp.ws.Name != workspace {
			continue
		}
		if i := slices.IndexFunc(sp.repos, func(r *repo) bool { return r.cfg.Name == name }); i >= 0 {
			return sp.repos[i]
		}
	}
	return nil
}

// status describes every served repo.
func (g *generation) status() daemon.Status {
	var st daemon.Status
	for _, sp := range g.spaces {
		ws := daemon.WorkspaceStatus{Name: sp.ws.Name}
		for _, r := range sp.repos {
			ws.Repos = append(ws.Repos, r.status())
		}
		st.Workspaces = append(st.Workspaces, ws)
	}
	return st
}

// fresh answers the fresh op for a repo (see Service.Fresh).
func (g *generation) fresh(q daemon.FreshQuery) daemon.FreshReply {
	r := g.repo(q.Workspace, q.Repo)
	switch {
	case r == nil:
		return daemon.FreshReply{Note: "daemon: repo not served"}
	case q.Root != r.cfg.Path || q.DB != r.sp.dbPath():
		return daemon.FreshReply{Note: "daemon: serves " + r.cfg.Path + " with " + r.sp.dbPath()}
	case q.Version != buildinfo.Version || q.Extractor != index.ExtractorVersion():
		return daemon.FreshReply{Note: fmt.Sprintf("daemon is running another codedoc build (%s, extractor %s; this client: %s, extractor %s); restart it",
			buildinfo.Version, index.ExtractorVersion(), q.Version, q.Extractor)}
	case q.DBID != r.sp.dbID:
		return daemon.FreshReply{Note: "daemon has a different database open; restart it after recreating " + q.DB}
	}
	return r.freshReply()
}

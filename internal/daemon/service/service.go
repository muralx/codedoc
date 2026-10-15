// Package service is the codedoc daemon (spec section 12): it keeps the
// index of every configured repo current from file system events and
// branch switches, embeds what it re-extracts with a model it keeps
// loaded, answers status and freshness questions on the Unix socket,
// runs SCIP and autodoc on their schedules, and documents what a branch
// changed once its repo is clean and quiet (upkeep, efficiency spec
// section 7). Work on one repo never stops
// the others: errors and panics are logged against the repo and retried
// on its next event, and a repo whose changes may be missed (a missing
// directory, a watch that failed) is reported degraded, never fresh.
package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"sync"
	"time"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/daemon"
	"github.com/muralx/codedoc/internal/embed"
)

// Options configure a Service; zero values take the defaults.
type Options struct {
	// Config is the loaded configuration; required.
	Config *config.Config
	// ConfigPath is the file Config was read from; when set, the daemon
	// reloads it once its content changes (see watchConfig).
	ConfigPath string
	// ConfigPoll is how often ConfigPath is checked; default 2 s.
	ConfigPoll time.Duration
	// Workspace limits the daemon to one workspace; "" serves them all.
	Workspace string
	// Log receives every event; required.
	Log *daemon.Logger
	// SocketPath is where status is served; default daemon.SocketPath().
	SocketPath string
	// Debounce and HeadInterval tune the watcher (see daemon.WatchOptions).
	Debounce, HeadInterval time.Duration
	// Now is the clock schedules are computed from; default time.Now.
	Now func() time.Time
	// Tick is how often due schedules are checked; default 30 s.
	Tick time.Duration
	// ClaudeBin is the claude binary scheduled autodoc runs invoke;
	// default as autodoc.Options.ClaudeBin.
	ClaudeBin string
	// AutodocMinutes bounds one scheduled autodoc run; default 30.
	AutodocMinutes int
}

// Service is a daemon over the configured workspaces. New claims the
// data directory and the socket; Run serves until its context ends.
type Service struct {
	opts      Options
	log       *daemon.Logger
	embedder  embed.Embedder
	ln        net.Listener
	unlock    func() error
	closeOnce sync.Once

	mu  sync.Mutex
	gen *generation // nil while none is open
}

// New checks the workspace selection, takes the single-instance lock of
// the data directory and listens on the socket, so a second daemon fails
// here, at once, before any indexing. Run (or Close) releases both.
func New(opts Options) (*Service, error) {
	opts = withDefaults(opts)
	names, err := workspaceNames(opts.Config, opts.Workspace)
	if err != nil {
		return nil, err
	}
	unlock, err := daemon.LockInstance(filepath.Dir(opts.Config.DBPath(names[0])))
	if err != nil {
		return nil, err
	}
	ln, err := daemon.Listen(opts.SocketPath)
	if err != nil {
		return nil, errors.Join(err, unlock())
	}
	s := &Service{opts: opts, log: opts.Log, ln: ln, unlock: unlock}
	s.embedder = embed.Configured(opts.Config.Embed.ModelDir, logWriter{opts.Log})
	return s, nil
}

func withDefaults(o Options) Options {
	if o.SocketPath == "" {
		o.SocketPath = daemon.SocketPath()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Tick <= 0 {
		o.Tick = 30 * time.Second
	}
	if o.AutodocMinutes <= 0 {
		o.AutodocMinutes = 30
	}
	if o.ConfigPoll <= 0 {
		o.ConfigPoll = 2 * time.Second
	}
	return o
}

// workspaceNames returns the workspaces of cfg to serve, sorted.
func workspaceNames(cfg *config.Config, only string) ([]string, error) {
	if only != "" {
		if _, ok := cfg.Workspaces[only]; !ok {
			return nil, fmt.Errorf("unknown workspace %q", only)
		}
		return []string{only}, nil
	}
	names := cfg.Names()
	if len(names) == 0 {
		return nil, errors.New("no workspace configured")
	}
	return names, nil
}

// Run serves the socket at once, then opens the workspaces, brings every
// repo up to date, and indexes changes as they come and runs due
// schedules; when the configuration file changes it closes the
// workspaces and opens them again from the new file. On cancellation it
// lets work in progress finish, closes the socket and the databases, and
// returns nil. When the socket cannot be served or the watcher fails it
// shuts down the same way and returns the error.
func (s *Service) Run(ctx context.Context) error {
	defer func() { _ = s.Close() }()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	var serveErr error
	wg.Go(func() {
		serveErr = daemon.ServeListener(ctx, s.ln, s)
		cancel()
	})
	s.log.Printf("daemon: listening on %s", s.opts.SocketPath)
	runErr := s.generations(ctx)
	cancel()
	wg.Wait()
	s.log.Printf("daemon: stopped")
	return errors.Join(serveErr, runErr)
}

// Close releases the socket, the instance lock and the embedder; Run
// calls it on return. It is safe to call more than once.
func (s *Service) Close() error {
	var err error
	s.closeOnce.Do(func() {
		_ = s.ln.Close()
		if s.embedder != nil {
			_ = s.embedder.Close()
		}
		err = s.unlock()
	})
	return err
}

// current returns the open generation, or nil.
func (s *Service) current() *generation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gen
}

func (s *Service) setCurrent(g *generation) {
	s.mu.Lock()
	s.gen = g
	s.mu.Unlock()
}

// Status implements daemon.StatusProvider.
func (s *Service) Status() daemon.Status {
	if g := s.current(); g != nil {
		return g.status()
	}
	return daemon.Status{}
}

// Fresh implements daemon.StatusProvider: a repo is fresh once its first
// catch-up succeeded, while no change is waiting or being indexed, the
// last indexing did not fail and it is not degraded, and only when the
// asking process uses the repo directory and database the daemon keeps.
func (s *Service) Fresh(q daemon.FreshQuery) daemon.FreshReply {
	g := s.current()
	if g == nil {
		return daemon.FreshReply{Note: "daemon: starting"}
	}
	return g.fresh(q)
}

// logWriter turns writes into log lines.
type logWriter struct{ log *daemon.Logger }

func (w logWriter) Write(p []byte) (int, error) {
	w.log.Printf("%s", trimNewline(string(p)))
	return len(p), nil
}

func trimNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

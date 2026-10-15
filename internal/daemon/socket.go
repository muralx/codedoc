package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/muralx/codedoc/internal/xdg"
)

// maxRequest caps the length of one request line.
const maxRequest = 64 << 10

// ErrNoDaemon reports that no daemon listens on the socket.
var ErrNoDaemon = errors.New("daemon: not running")

// ErrSocketInUse reports that another daemon already listens on the
// socket path.
var ErrSocketInUse = errors.New("daemon: socket already in use")

// Status is the daemon's answer to the status op.
type Status struct {
	Workspaces []WorkspaceStatus `json:"workspaces"`
}

// WorkspaceStatus describes the repos of one workspace.
type WorkspaceStatus struct {
	Name  string       `json:"name"`
	Repos []RepoStatus `json:"repos"`
}

// RepoStatus describes the index state and schedule of one repo.
type RepoStatus struct {
	Name      string    `json:"name"`
	Branch    string    `json:"branch,omitempty"`
	Commit    string    `json:"commit,omitempty"`
	IndexedAt time.Time `json:"indexed_at,omitzero"`
	// Queue is the number of changed paths waiting to be indexed.
	Queue     int    `json:"queue"`
	LastError string `json:"last_error,omitempty"`
	// Fresh is the daemon's freshness answer for the repo (see
	// FreshReply).
	Fresh bool `json:"fresh"`
	// Degraded says why the daemon may miss changes of the repo.
	Degraded string `json:"degraded,omitempty"`
	// Missing reports that the repo's directory is gone; its index is
	// kept.
	Missing bool `json:"missing,omitempty"`
	// NextSCIP and NextAutodoc are the next scheduled runs; zero when
	// none is scheduled.
	NextSCIP    time.Time `json:"next_scip,omitzero"`
	NextAutodoc time.Time `json:"next_autodoc,omitzero"`
	// Upkeep is the repo's documentation upkeep.
	Upkeep UpkeepStatus `json:"upkeep"`
}

// UpkeepStatus describes the documentation upkeep of one repo
// (efficiency spec section 7).
type UpkeepStatus struct {
	// Enabled reports whether the repo's workspace opted in.
	Enabled bool `json:"enabled"`
	// Running reports a run in progress.
	Running bool `json:"running,omitempty"`
	// Pending counts the candidates found by the last check: changed
	// symbols whose doc is stale or missing.
	Pending int `json:"pending"`
	// Waiting says why no run starts although upkeep is enabled, such
	// as "uncommitted changes"; "" when nothing holds it back.
	Waiting string `json:"waiting,omitempty"`
	// CapReached reports that the workspace spent its daily cap, which
	// stops upkeep until the next day; SpentToday is that spend in USD.
	CapReached bool    `json:"daily_cap_reached,omitempty"`
	SpentToday float64 `json:"spent_today_usd,omitempty"`
	// LastRun is when a run last finished; zero when none did.
	LastRun time.Time `json:"last_run,omitzero"`
	// LastError is why the last check or run failed or was undone; a
	// run that finishes clears it.
	LastError string `json:"last_error,omitempty"`
}

// FreshReply answers the fresh op: Fresh is true when the repo's index
// matches its working tree, and Pending counts changes not yet indexed.
// Note says why a repo is not fresh when that is not just pending work.
type FreshReply struct {
	Fresh   bool   `json:"fresh"`
	Pending int    `json:"pending"`
	Note    string `json:"note,omitempty"`
}

// FreshQuery names the repo a fresh op asks about. Root and DB are the
// repo directory and workspace database the asking process uses, and
// DBID the device and inode of that database file: the daemon answers
// fresh only when they are the ones it keeps current, so a client with
// another configuration, or one that recreated the database the daemon
// has open, never trusts it. Build it with NewFreshQuery.
type FreshQuery struct {
	Workspace string `json:"workspace,omitempty"`
	Repo      string `json:"repo,omitempty"`
	Root      string `json:"root,omitempty"`
	DB        string `json:"db,omitempty"`
	DBID      FileID `json:"db_id,omitzero"`
	// Version is the client's build and Extractor the version of the
	// extracted rows it writes: a daemon of another build may index
	// differently, so it answers not fresh to them.
	Version   string `json:"version,omitempty"`
	Extractor string `json:"extractor,omitempty"`
}

// StatusProvider answers socket requests; it must be safe for concurrent
// use.
type StatusProvider interface {
	Status() Status
	Fresh(q FreshQuery) FreshReply
}

// request is one line sent by a client.
type request struct {
	Op string `json:"op"`
	FreshQuery
}

// errorReply is the answer to a request the daemon cannot serve.
type errorReply struct {
	Error string `json:"error"`
}

// SocketPath returns $XDG_RUNTIME_DIR/codedoc.sock, or
// ${XDG_STATE_HOME:-~/.local/state}/codedoc/codedoc.sock when no runtime
// directory is set.
func SocketPath() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); filepath.IsAbs(dir) {
		return filepath.Join(dir, "codedoc.sock")
	}
	state, _ := xdg.StateDir()
	return filepath.Join(state, "codedoc.sock")
}

// ServeListener answers JSON-lines requests from p on ln, from Listen,
// until ctx is cancelled, then closes the listener and every open
// connection, waits for their handlers, and returns nil.
func ServeListener(ctx context.Context, ln net.Listener, p StatusProvider) error {
	s := &server{p: p, conns: map[net.Conn]bool{}}
	stop := context.AfterFunc(ctx, func() { s.shutdown(ln) })
	defer stop()
	for {
		c, err := ln.Accept()
		if err != nil {
			s.shutdown(ln)
			s.wg.Wait()
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if !s.track(c) {
			_ = c.Close()
			continue
		}
		go s.handle(c)
	}
}

// maxSocketPath is the longest Unix socket path Linux accepts.
const maxSocketPath = 107

// Listen removes a stale socket at path and listens there with mode
// 0600. A socket another process listens on yields ErrSocketInUse.
func Listen(path string) (net.Listener, error) {
	return listen(path, dialTimeout)
}

// listen is Listen with probe bounding the liveness probe of an existing
// socket.
func listen(path string, probe time.Duration) (net.Listener, error) {
	if len(path) > maxSocketPath {
		return nil, fmt.Errorf("daemon: socket path %s is longer than %d bytes; set XDG_RUNTIME_DIR to a shorter directory",
			path, maxSocketPath)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := removeStale(path, probe); err != nil {
		return nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, errors.Join(err, ln.Close())
	}
	return ln, nil
}

// removeStale deletes the socket file at path only when connecting to
// it is refused, which proves nobody listens there. Any other probe
// failure, a timeout on a loaded machine included, counts as a live
// daemon: deleting its socket would orphan it. A non-socket file at
// path is left alone and reported. probe bounds the connection attempt.
func removeStale(path string, probe time.Duration) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode().Type() != os.ModeSocket {
		return fmt.Errorf("daemon: %s exists and is not a socket", path)
	}
	c, err := net.DialTimeout("unix", path, probe)
	switch {
	case err == nil:
		_ = c.Close()
		return fmt.Errorf("%w: %s", ErrSocketInUse, path)
	case errors.Is(err, syscall.ECONNREFUSED):
		return os.Remove(path)
	case errors.Is(err, fs.ErrNotExist):
		return nil
	default:
		return fmt.Errorf("%w: %s (probe: %v)", ErrSocketInUse, path, err)
	}
}

// server tracks open connections so shutdown can close them.
type server struct {
	p      StatusProvider
	wg     sync.WaitGroup
	mu     sync.Mutex
	closed bool
	conns  map[net.Conn]bool
}

// track registers c; it returns false once the server is shutting down.
func (s *server) track(c net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.conns[c] = true
	s.wg.Add(1)
	return true
}

func (s *server) untrack(c net.Conn) {
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
	_ = c.Close()
	s.wg.Done()
}

// shutdown closes the listener and every open connection.
func (s *server) shutdown(ln net.Listener) {
	s.mu.Lock()
	s.closed = true
	for c := range s.conns {
		_ = c.Close()
	}
	s.mu.Unlock()
	_ = ln.Close()
}

// handle answers each request line of c until the client hangs up.
func (s *server) handle(c net.Conn) {
	defer s.untrack(c)
	sc := bufio.NewScanner(c)
	sc.Buffer(make([]byte, 0, 4096), maxRequest)
	enc := json.NewEncoder(c)
	for sc.Scan() {
		if err := enc.Encode(s.answer(sc.Bytes())); err != nil {
			return
		}
	}
	if errors.Is(sc.Err(), bufio.ErrTooLong) {
		_ = enc.Encode(errorReply{Error: "request too long"})
	}
}

// answer returns the reply to one request line.
func (s *server) answer(line []byte) any {
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		return errorReply{Error: "malformed request: " + err.Error()}
	}
	switch req.Op {
	case "status":
		return s.p.Status()
	case "fresh":
		if req.Workspace == "" || req.Repo == "" {
			return errorReply{Error: "fresh needs workspace and repo"}
		}
		return s.p.Fresh(req.FreshQuery)
	default:
		return errorReply{Error: fmt.Sprintf("unknown op %q", req.Op)}
	}
}

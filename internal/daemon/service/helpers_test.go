package service

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/daemon"
	"github.com/muralx/codedoc/internal/embed"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/query"
	"github.com/muralx/codedoc/internal/store"
	"github.com/muralx/codedoc/internal/testutil"
)

const (
	// latency is how soon an edit must be visible to queries.
	latency = 2 * time.Second
	// waitLimit bounds every other wait.
	waitLimit = 20 * time.Second
)

// fakeClock is a settable clock for schedules.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// lockedBuffer collects log output safely.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// harness is a running service over copies of fixture repos.
type harness struct {
	t       *testing.T
	cfg     *config.Config
	cfgPath string
	repos   map[string]string
	clock   *fakeClock
	log     *lockedBuffer
	socket  string
	cancel  context.CancelFunc
	done    chan error
}

// newHarness copies the named fixtures into workspace fx, whose config
// gets extra appended under the workspace, and loads the config.
func newHarness(t *testing.T, extra string, names ...string) *harness {
	t.Helper()
	h := &harness{t: t, repos: map[string]string{}, log: &lockedBuffer{},
		clock: &fakeClock{now: time.Date(2026, 10, 9, 12, 0, 0, 0, time.Local)}}
	var list []string
	for _, n := range names {
		h.repos[n] = testutil.CopyFixture(t, n)
		list = append(list, "{path: "+h.repos[n]+"}")
	}
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	body := "workspaces:\n  fx:\n    repos: [" + strings.Join(list, ", ") + "]\n" + extra
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEDOC_DATA", filepath.Join(t.TempDir(), "data"))
	t.Setenv("CODEDOC_EMBEDDER", "fake")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	h.cfg, h.cfgPath = cfg, cfgPath
	dir, err := os.MkdirTemp("", "cds")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	h.socket = filepath.Join(dir, "d.sock")
	return h
}

// options returns the service options of the harness.
func (h *harness) options() Options {
	log, err := daemon.NewLogger(h.log, "")
	if err != nil {
		h.t.Fatal(err)
	}
	return Options{Config: h.cfg, ConfigPath: h.cfgPath, ConfigPoll: 50 * time.Millisecond, Log: log,
		SocketPath: h.socket, Debounce: 50 * time.Millisecond, HeadInterval: 50 * time.Millisecond,
		Now: h.clock.Now, Tick: 10 * time.Millisecond}
}

// start runs a service with opts until the test ends and waits until
// every repo caught up.
func (h *harness) start(opts Options) *Service {
	h.t.Helper()
	s, err := New(opts)
	if err != nil {
		h.t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel, h.done = cancel, make(chan error, 1)
	go func() { h.done <- s.Run(ctx) }()
	h.t.Cleanup(func() { _ = h.stop() })
	for name := range h.repos {
		h.eventually("catch-up of "+name, waitLimit, func() bool {
			r, err := daemon.QueryFresh(h.socket, h.query(name))
			return err == nil && r.Fresh
		})
	}
	return s
}

// query is the fresh query of a repo of fx, as the CLI sends it.
func (h *harness) query(name string) daemon.FreshQuery {
	q := daemon.NewFreshQuery("fx", name, h.repos[name], h.cfg.DBPath("fx"))
	q.Extractor = index.ExtractorVersion()
	return q
}

// stop cancels the service and returns what Run returned.
func (h *harness) stop() error {
	h.cancel()
	select {
	case err := <-h.done:
		h.done <- nil
		return err
	case <-time.After(waitLimit):
		h.t.Error("service did not stop")
		return nil
	}
}

// eventually polls cond until it holds or limit passes.
func (h *harness) eventually(what string, limit time.Duration, cond func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(limit)
	for !cond() {
		if time.Now().After(deadline) {
			h.t.Fatalf("timed out after %v waiting for %s\nlog:\n%s", limit, what, h.log.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// find runs the find query on what the daemon indexed: the daemon is
// trusted to be fresh, so the query does no indexing of its own.
func (h *harness) find(name string) string {
	h.t.Helper()
	s, err := store.Open(h.cfg.DBPath("fx"))
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	c := &query.Ctx{Store: s, Workspace: h.cfg.Workspaces["fx"], DaemonFresh: func(string, *config.Repo) bool { return true }}
	resp, err := query.Find(c, name, query.FindOpts{})
	if err != nil {
		h.t.Fatal(err)
	}
	var b bytes.Buffer
	if _, err := output.Render(&b, resp, "text", 0); err != nil {
		h.t.Fatal(err)
	}
	return b.String()
}

// write writes a file of a repo.
func (h *harness) write(repo, rel, body string) {
	h.t.Helper()
	p := filepath.Join(h.repos[repo], filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

// git runs git in a repo.
func (h *harness) git(repo string, args ...string) {
	h.t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@x", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = h.repos[repo]
	if out, err := cmd.CombinedOutput(); err != nil {
		h.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// repoStatus returns the socket status of a repo of fx.
func (h *harness) repoStatus(name string) daemon.RepoStatus {
	h.t.Helper()
	st, err := daemon.QueryStatus(h.socket)
	if err != nil {
		h.t.Fatal(err)
	}
	for _, ws := range st.Workspaces {
		for _, r := range ws.Repos {
			if ws.Name == "fx" && r.Name == name {
				return r
			}
		}
	}
	h.t.Fatalf("no status for %s: %+v", name, st)
	return daemon.RepoStatus{}
}

// vectors counts the embedded and the documented symbols and modules of
// a repo.
func (h *harness) vectors(repo string) (embedded, documented int) {
	h.t.Helper()
	s, err := store.Open(h.cfg.DBPath("fx"))
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	row, err := s.RepoByPath(h.repos[repo])
	if err != nil {
		h.t.Fatal(err)
	}
	embedded, documented, err = index.VectorCounts(s, row.ID, embed.FakeDims)
	if err != nil {
		h.t.Fatal(err)
	}
	return embedded, documented
}

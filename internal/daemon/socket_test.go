package daemon

import (
	"bufio"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeProvider answers with fixed values.
type fakeProvider struct{ at time.Time }

func (f fakeProvider) Status() Status {
	return Status{Workspaces: []WorkspaceStatus{{Name: "w", Repos: []RepoStatus{
		{Name: "r", Branch: "main", Commit: "abc", IndexedAt: f.at, Queue: 2, LastError: "boom", NextSCIP: f.at.Add(time.Hour)},
	}}}}
}

func (f fakeProvider) Fresh(q FreshQuery) FreshReply {
	if q.Workspace == "w" && q.Repo == "r" && q.Root == "/r" && q.DB == "/w.db" {
		return FreshReply{Fresh: true}
	}
	return FreshReply{Pending: 3, Note: "other"}
}

// sockPath returns a short socket path (Unix socket paths are limited
// to about 100 bytes).
func sockPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "cdsock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}

// startServer serves fakeProvider at path; the returned stop cancels the
// server and returns Serve's result.
func startServer(t *testing.T, path string) func() error {
	t.Helper()
	ctx, cancel := context.WithCancel(testCtx(t, 10*waitLimit))
	done := make(chan error, 1)
	go func() { done <- serve(ctx, path, fakeProvider{at: time.Unix(1700000000, 0).UTC()}) }()
	deadline := time.Now().Add(bounded(t, waitLimit))
	for {
		if c, err := net.Dial("unix", path); err == nil {
			_ = c.Close()
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("server did not start: %v", receive(t, done, waitLimit, "Serve to return"))
		}
		time.Sleep(5 * time.Millisecond)
	}
	var once sync.Once
	var result error
	stop := func() error {
		once.Do(func() {
			cancel()
			select {
			case result = <-done:
			case <-time.After(bounded(t, waitLimit)):
				result = errors.New("serve did not return")
			}
		})
		return result
	}
	t.Cleanup(func() { _ = stop() })
	return stop
}

func TestSocketPath(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/7")
	if got := SocketPath(); got != "/run/user/7/codedoc.sock" {
		t.Errorf("runtime dir: %s", got)
	}
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("XDG_STATE_HOME", "/st")
	if got := SocketPath(); got != "/st/codedoc/codedoc.sock" {
		t.Errorf("state dir: %s", got)
	}
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/h")
	if got := SocketPath(); got != "/h/.local/state/codedoc/codedoc.sock" {
		t.Errorf("home: %s", got)
	}
}

func TestSocketOps(t *testing.T) {
	path := sockPath(t)
	startServer(t, path)
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode: %v %v", info, err)
	}
	st, err := QueryStatus(path)
	if err != nil {
		t.Fatal(err)
	}
	checkRepoStatus(t, st.Workspaces[0].Repos[0])
	checkFresh(t, path)
}

func checkRepoStatus(t *testing.T, r RepoStatus) {
	t.Helper()
	if r.Branch != "main" || r.Queue != 2 || r.LastError != "boom" || !r.IndexedAt.Equal(time.Unix(1700000000, 0)) || r.NextSCIP.IsZero() || !r.NextAutodoc.IsZero() {
		t.Errorf("status: %+v", r)
	}
}

func checkFresh(t *testing.T, path string) {
	t.Helper()
	if f, err := QueryFresh(path, FreshQuery{Workspace: "w", Repo: "r", Root: "/r", DB: "/w.db"}); err != nil || !f.Fresh || f.Pending != 0 {
		t.Errorf("fresh w/r: %+v %v", f, err)
	}
	if f, err := QueryFresh(path, FreshQuery{Workspace: "w", Repo: "r", Root: "/elsewhere", DB: "/w.db"}); err != nil || f.Fresh || f.Note != "other" {
		t.Errorf("fresh w/r elsewhere: %+v %v", f, err)
	}
	if f, err := QueryFresh(path, FreshQuery{Workspace: "w", Repo: "x"}); err != nil || f.Fresh || f.Pending != 3 {
		t.Errorf("fresh w/x: %+v %v", f, err)
	}
	if _, err := QueryFresh(path, FreshQuery{Repo: "x"}); err == nil || !strings.Contains(err.Error(), "workspace and repo") {
		t.Errorf("fresh without workspace: %v", err)
	}
}

func TestSocketProtocolErrors(t *testing.T) {
	path := sockPath(t)
	startServer(t, path)
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if err := c.SetDeadline(time.Now().Add(bounded(t, waitLimit))); err != nil {
		t.Fatal(err)
	}
	rd := bufio.NewReader(c)
	for _, tc := range []struct{ send, want string }{
		{"not json\n", `{"error":"malformed request:`},
		{`{"op":"nope"}` + "\n", `{"error":"unknown op \"nope\""}`},
		{`{"op":"fresh","workspace":"w","repo":"r","root":"/r","db":"/w.db"}` + "\n", `{"fresh":true,"pending":0}`},
		{strings.Repeat("x", maxRequest+1) + "\n", `{"error":"request too long"}`},
	} {
		if _, err := c.Write([]byte(tc.send)); err != nil {
			t.Fatal(err)
		}
		line, err := rd.ReadString('\n')
		if err != nil || !strings.HasPrefix(line, tc.want) {
			t.Errorf("send %.20q: got %q %v, want prefix %s", tc.send, line, err, tc.want)
		}
	}
}

func TestSocketConcurrentClients(t *testing.T) {
	path := sockPath(t)
	startServer(t, path)
	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i := range 40 {
		wg.Go(func() {
			if i%2 == 0 {
				_, err := QueryStatus(path)
				errs <- err
				return
			}
			_, err := QueryFresh(path, FreshQuery{Workspace: "w", Repo: "r"})
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
}

func TestSocketStaleAndInUse(t *testing.T) {
	path := sockPath(t)
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = ln.Close() // leaves a stale socket file behind
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stale socket missing: %v", err)
	}
	startServer(t, path)
	if _, err := QueryStatus(path); err != nil {
		t.Fatalf("after replacing stale socket: %v", err)
	}
	err = serve(testCtx(t, waitLimit), path, fakeProvider{})
	if !errors.Is(err, ErrSocketInUse) {
		t.Errorf("second server: %v", err)
	}
}

func TestSocketRefusesNonSocket(t *testing.T) {
	path := sockPath(t)
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := serve(testCtx(t, waitLimit), path, fakeProvider{}); err == nil || !strings.Contains(err.Error(), "not a socket") {
		t.Errorf("serve over a regular file: %v", err)
	}
}

func TestSocketShutdown(t *testing.T) {
	path := sockPath(t)
	stop := startServer(t, path)
	idle, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idle.Close() }()
	if err := stop(); err != nil {
		t.Fatalf("shutdown with an idle client: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("socket file left behind: %v", err)
	}
	if _, err := QueryStatus(path); !errors.Is(err, ErrNoDaemon) {
		t.Errorf("after shutdown: %v", err)
	}
}

func TestServeRejectsLongSocketPath(t *testing.T) {
	long := filepath.Join(t.TempDir(), strings.Repeat("d", 120), "codedoc.sock")
	err := serve(t.Context(), long, nil)
	if err == nil || !strings.Contains(err.Error(), "set XDG_RUNTIME_DIR to a shorter directory") {
		t.Fatal(err)
	}
}

func TestLockInstance(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	release, err := LockInstance(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LockInstance(dir); !errors.Is(err, ErrRunning) || !strings.Contains(err.Error(), "daemon.lock") {
		t.Fatalf("second lock: %v", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	again, err := LockInstance(dir)
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	_ = again()
	if _, err := LockInstance("/proc/no/such/dir"); err == nil {
		t.Fatal("unwritable dir locked")
	}
}

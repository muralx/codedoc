package daemon

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testDebounce = 50 * time.Millisecond
	// latencyLimit is the delivery latency the design promises.
	latencyLimit = time.Second
	// waitLimit bounds every wait for a delivery, a server or a
	// shutdown. It is generous so that a loaded machine makes a test
	// slow, not failing; a wait that runs out fails the test with a
	// message instead of hanging until the go test timeout.
	waitLimit = 10 * time.Second
	// gitLimit bounds one git command run by a test.
	gitLimit = 30 * time.Second
)

// bounded shortens d so that it ends a few seconds before the test
// binary's -timeout, leaving time to report the failure.
func bounded(t *testing.T, d time.Duration) time.Duration {
	t.Helper()
	dl, ok := t.Deadline()
	if !ok {
		return d
	}
	return max(min(d, time.Until(dl)-5*time.Second), 100*time.Millisecond)
}

// testCtx returns a context cancelled after bounded(t, d) or when the
// test ends.
func testCtx(t *testing.T, d time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), bounded(t, d))
	t.Cleanup(cancel)
	return ctx
}

// eventually polls cond until it holds, failing the test after
// waitLimit.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(bounded(t, waitLimit))
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// receive waits for one value from ch, failing the test after limit.
func receive[T any](t *testing.T, ch <-chan T, limit time.Duration, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(bounded(t, limit)):
		t.Fatalf("timed out after %v waiting for %s", limit, what)
		panic("unreachable")
	}
}

// git runs git in dir and fails the test on error.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(testCtx(t, gitLimit), "git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com",
		"-c", "init.defaultBranch=main", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, stderr.String())
	}
	return strings.TrimSpace(string(out))
}

// newRepo creates a git repo with one committed file per name.
func newRepo(t *testing.T, files ...string) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	git(t, root, "init", "-q")
	for _, f := range files {
		write(t, root, f, "x\n")
	}
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "init")
	return root
}

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// collector runs a Watcher and records what it delivers.
type collector struct {
	t       *testing.T
	w       *Watcher
	mu      sync.Mutex
	changes []Change
	notify  chan struct{}
}

// lockedBuffer is a log sink safe for concurrent use.
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

func (b *lockedBuffer) contains(s string) bool { return strings.Contains(b.String(), s) }

// start watches the repos and records deliveries, logging to logW;
// deliver, when not nil, runs after recording. The test fails if Run
// does not return within waitLimit of the test's end.
func start(t *testing.T, repos []RepoSpec, logW io.Writer, deliver func(context.Context, Change)) *collector {
	t.Helper()
	log := &Logger{stderr: logW, max: maxLogSize}
	w, err := NewWatcher(repos, WatchOptions{Debounce: testDebounce, HeadInterval: testDebounce, Log: log,
		Manifest: func(rel string) bool { return strings.HasSuffix(rel, ".lock") }})
	if err != nil {
		t.Fatal(err)
	}
	c := &collector{t: t, w: w, notify: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- w.Run(ctx, func(ctx context.Context, ch Change) {
			c.mu.Lock()
			c.changes = append(c.changes, ch)
			c.mu.Unlock()
			select {
			case c.notify <- struct{}{}:
			default:
			}
			if deliver != nil {
				deliver(ctx, ch)
			}
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("run: %v", err)
			}
		case <-time.After(bounded(t, waitLimit)):
			t.Errorf("Run did not return within %v of cancellation", waitLimit)
		}
	})
	return c
}

func startRepo(t *testing.T, root string, exclude ...string) *collector {
	t.Helper()
	return start(t, []RepoSpec{{Workspace: "w", Name: "r", Root: root, Exclude: exclude}}, io.Discard, nil)
}

// waitFor waits up to waitLimit until match accepts the changes so far.
func (c *collector) waitFor(what string, match func([]Change) bool) {
	c.t.Helper()
	deadline := time.After(bounded(c.t, waitLimit))
	for {
		c.mu.Lock()
		ok := match(c.changes)
		c.mu.Unlock()
		if ok {
			return
		}
		select {
		case <-c.notify:
		case <-deadline:
			c.t.Fatalf("timed out waiting for %s; got %+v", what, c.snapshot())
		}
	}
}

func (c *collector) waitPath(p string) {
	c.t.Helper()
	c.waitFor(p, func(cs []Change) bool { return hasPath(cs, p) })
}

func (c *collector) snapshot() []Change {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.changes)
}

// settle waits a few debounce periods and returns the changes so far.
func (c *collector) settle() []Change {
	time.Sleep(6 * testDebounce)
	return c.snapshot()
}

func hasPath(cs []Change, p string) bool {
	for _, c := range cs {
		if slices.Contains(c.Paths, p) {
			return true
		}
	}
	return false
}

func quietLogger() *Logger { return &Logger{stderr: io.Discard, max: maxLogSize} }

// serve listens on the socket at path and serves p there until ctx ends,
// as the daemon service does.
func serve(ctx context.Context, path string, p StatusProvider) error {
	ln, err := Listen(path)
	if err != nil {
		return err
	}
	return ServeListener(ctx, ln, p)
}

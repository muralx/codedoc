package daemon

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestServeKeepsLiveSocketWhenProbeTimesOut covers the hang seen under
// load: a liveness probe that timed out used to count as "stale", so a
// second Serve deleted a live daemon's socket and served forever.
func TestServeKeepsLiveSocketWhenProbeTimesOut(t *testing.T) {
	path := sockPath(t)
	startServer(t, path)
	done := make(chan error, 1)
	go func() {
		_, err := listen(path, time.Nanosecond) // the probe cannot finish in time
		done <- err
	}()
	err := receive(t, done, 2*time.Second, "second Serve to give up on a live socket")
	if !errors.Is(err, ErrSocketInUse) {
		t.Fatalf("second Serve: %v, want ErrSocketInUse", err)
	}
	if _, err := QueryStatus(path); err != nil {
		t.Fatalf("first daemon lost its socket: %v", err)
	}
}

// TestGitIgnoredBoundedByContext runs gitIgnored against a git that
// never exits; the context must stop it.
func TestGitIgnoredBoundedByContext(t *testing.T) {
	bin := t.TempDir()
	write(t, bin, "git", "#!/bin/sh\nexec sleep 600\n")
	if err := os.Chmod(filepath.Join(bin, "git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := gitIgnored(ctx, t.TempDir(), []string{"a"})
		done <- err
	}()
	if err := receive(t, done, waitLimit, "gitIgnored to give up on a stuck git"); err == nil {
		t.Fatal("expected an error from a git that was stopped")
	}
}

// TestRunCancelsBlockedDeliver checks that a deliver waiting on its
// context lets Run return once Run's context ends.
func TestRunCancelsBlockedDeliver(t *testing.T) {
	root := newRepo(t, "a.go")
	w, err := NewWatcher([]RepoSpec{{Workspace: "w", Name: "r", Root: root}},
		WatchOptions{Debounce: testDebounce, HeadInterval: testDebounce, Log: quietLogger()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(testCtx(t, waitLimit))
	defer cancel()
	entered := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- w.Run(ctx, func(ctx context.Context, _ Change) {
			close(entered)
			<-ctx.Done()
		})
	}()
	write(t, root, "a.go", "edited\n")
	receive(t, entered, waitLimit, "the first delivery")
	cancel()
	if err := receive(t, done, waitLimit, "Run to return after cancellation"); err != nil {
		t.Fatalf("run: %v", err)
	}
}

// TestRemoveStaleProbeOutcomes checks that only a refused connection
// marks a socket file stale.
func TestRemoveStaleProbeOutcomes(t *testing.T) {
	path := sockPath(t)
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	if err := removeStale(path, dialTimeout); !errors.Is(err, ErrSocketInUse) {
		t.Fatalf("live socket: %v", err)
	}
	_ = ln.Close()
	if err := removeStale(path, dialTimeout); err != nil {
		t.Fatalf("stale socket: %v", err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("stale socket kept: %v", err)
	}
}

// TestRenameThenDeleteDoesNotDeadlock renames watched directories and
// deletes them at once, many times. fsnotify reports a failed watch
// removal on Errors while holding its own lock; the event loop must not
// be the only Errors reader while it waits for that lock in Add or
// Remove, or the watcher stops for good.
func TestRenameThenDeleteDoesNotDeadlock(t *testing.T) {
	root := newRepo(t, "a.go")
	c := startRepo(t, root)
	for i := range 300 {
		src := filepath.Join(root, "d")
		write(t, root, "d/sub/x.go", "x\n")
		eventually(t, "d/sub to be watched (iteration "+strconv.Itoa(i)+")", func() bool {
			return c.w.watched(filepath.Join(src, "sub"))
		})
		dst := filepath.Join(root, "moved")
		if err := os.Rename(src, dst); err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(dst); err != nil {
			t.Fatal(err)
		}
	}
	write(t, root, "a.go", "edited\n")
	c.waitPath("a.go")
}

// TestErrorsReadWhileEventLoopBusy keeps the event loop inside a git
// call that never returns and checks that fsnotify errors are still
// read: fsnotify sends them while holding the lock that Add and Remove
// need, so an event loop that is their only reader can deadlock.
func TestErrorsReadWhileEventLoopBusy(t *testing.T) {
	root := t.TempDir()
	bin := t.TempDir()
	mark := filepath.Join(bin, "started")
	write(t, bin, "git", "#!/bin/sh\ntouch '"+mark+"'\nexec sleep 600\n")
	if err := os.Chmod(filepath.Join(bin, "git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	log := &lockedBuffer{}
	c := start(t, []RepoSpec{{Workspace: "w", Name: "r", Root: root}}, log, nil)
	write(t, root, "newdir/x.go", "x\n") // the loop asks git about newdir
	eventually(t, "the event loop to call git", func() bool {
		_, err := os.Stat(mark)
		return err == nil
	})
	select {
	case c.w.fs.Errors <- errors.New("injected"):
	case <-time.After(bounded(t, 5*time.Second)):
		t.Fatal("fsnotify errors are not read while the event loop is busy")
	}
	eventually(t, "the injected error to be logged", func() bool { return log.contains("watch error: injected") })
}

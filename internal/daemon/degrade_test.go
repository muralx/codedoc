package daemon

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

// rescanned reports whether cs hold a rescan.
func rescanned(cs []Change) bool {
	for _, c := range cs {
		if c.Rescan {
			return true
		}
	}
	return false
}

// lastRescan returns the last rescan of cs.
func lastRescan(cs []Change) Change {
	for i := len(cs) - 1; i >= 0; i-- {
		if cs[i].Rescan {
			return cs[i]
		}
	}
	return Change{}
}

func TestMissingRootDegradesOnlyItsRepoUntilItAppears(t *testing.T) {
	good := newRepo(t, "a.go")
	gone := filepath.Join(t.TempDir(), "later")
	log := &lockedBuffer{}
	c := start(t, []RepoSpec{{Workspace: "w", Name: "good", Root: good}, {Workspace: "w", Name: "gone", Root: gone}}, log, nil)
	if d := c.w.Degraded("w", "gone"); !strings.Contains(d, "root missing: "+gone) || !c.w.Missing("w", "gone") {
		t.Fatalf("degraded %q", d)
	}
	if d := c.w.Degraded("w", "good"); d != "" {
		t.Fatalf("healthy repo degraded: %q", d)
	}
	write(t, good, "a.go", "edited\n")
	c.waitPath("a.go")
	write(t, gone, "b.go", "x\n")
	c.waitFor("a rescan of the reappeared root", rescanned)
	if c.w.Missing("w", "gone") {
		t.Fatal("still missing")
	}
	c.w.Recovered(lastRescan(c.snapshot()))
	if d := c.w.Degraded("w", "gone"); d != "" {
		t.Fatalf("degraded after the rescan: %q", d)
	}
	write(t, gone, "c.go", "x\n")
	c.waitPath("c.go")
}

func TestRemovedRootDegradesTheRepo(t *testing.T) {
	root := newRepo(t, "a.go")
	c := startRepo(t, root)
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the repo to degrade", func() bool { return c.w.Missing("w", "r") })
	if d := c.w.Degraded("w", "r"); !strings.Contains(d, "root missing") {
		t.Fatalf("degraded %q", d)
	}
	c.w.Recovered(Change{Workspace: "w", Repo: "r", Rescan: true})
	if c.w.Degraded("w", "r") == "" {
		t.Fatal("a missing root recovered")
	}
}

func TestWatchErrorsDegradeAndRescan(t *testing.T) {
	root := newRepo(t, "a.go")
	log := &lockedBuffer{}
	c := start(t, []RepoSpec{{Workspace: "w", Name: "r", Root: root}}, log, nil)
	for _, tc := range []struct {
		err    error
		reason string
	}{{errors.New("injected"), "watch error: injected"}, {fsnotify.ErrEventOverflow, "event queue overflow"}} {
		n := len(c.snapshot())
		c.w.fs.Errors <- tc.err
		c.waitFor("a rescan after "+tc.err.Error(), func(cs []Change) bool { return len(cs) > n && rescanned(cs[n:]) })
		if d := c.w.Degraded("w", "r"); !strings.Contains(d, tc.reason) {
			t.Fatalf("degraded %q, want %q", d, tc.reason)
		}
		stale := lastRescan(c.snapshot())
		stale.seq--
		c.w.Recovered(stale)
		if c.w.Degraded("w", "r") == "" {
			t.Fatal("an outdated rescan recovered the repo")
		}
		c.w.Recovered(lastRescan(c.snapshot()))
		if d := c.w.Degraded("w", "r"); d != "" {
			t.Fatalf("still degraded: %q", d)
		}
	}
	if !log.contains("w/r: degraded: event queue overflow") {
		t.Errorf("log: %s", log)
	}
}

func TestUnwatchableDirectoryDegradesTheRepo(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads any directory")
	}
	root := newRepo(t, "a.go", "locked/b.go", "open/c.go")
	locked := filepath.Join(root, "locked")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	log := &lockedBuffer{}
	c := start(t, []RepoSpec{{Workspace: "w", Name: "r", Root: root}}, log, nil)
	if d := c.w.Degraded("w", "r"); !strings.Contains(d, "cannot watch "+locked) {
		t.Fatalf("degraded %q", d)
	}
	if !log.contains("w/r: watch " + locked) {
		t.Errorf("log: %s", log)
	}
	write(t, root, "open/c.go", "edited\n")
	c.waitPath("open/c.go")
}

func TestOutOfWatchesTellsHowToRaiseTheLimit(t *testing.T) {
	w, err := NewWatcher(nil, WatchOptions{Log: quietLogger()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.fs.Close() }()
	r := newRepoState(RepoSpec{Workspace: "w", Name: "r", Root: t.TempDir()}, time.Second)
	w.repos = append(w.repos, r)
	if !w.watchFailed(r, "/x", syscall.ENOSPC) {
		t.Fatal("walk not stopped")
	}
	if d := w.Degraded("w", "r"); !strings.Contains(d, "fs.inotify.max_user_watches") {
		t.Fatalf("degraded %q", d)
	}
	if w.Degraded("w", "nope") != "" || w.Missing("w", "nope") {
		t.Fatal("unknown repo degraded")
	}
	w.Recovered(Change{Workspace: "w", Repo: "nope", Rescan: true})
}

func TestStreamOfEventsDeliveredWithinMaxWait(t *testing.T) {
	root := newRepo(t, "a.go")
	w, err := NewWatcher([]RepoSpec{{Workspace: "w", Name: "r", Root: root}},
		WatchOptions{Debounce: time.Second, HeadInterval: time.Hour, Log: &Logger{stderr: io.Discard, max: maxLogSize}})
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan time.Time, 8)
	ctx := testCtx(t, waitLimit)
	go func() { _ = w.Run(ctx, func(_ context.Context, _ Change) { got <- time.Now() }) }()
	start := time.Now()
	stop := time.After(bounded(t, 4*time.Second))
	for i := 0; ; i++ {
		select {
		case at := <-got:
			if d := at.Sub(start); d > maxWait+time.Second {
				t.Fatalf("first delivery after %s of continuous events", d)
			}
			return
		case <-stop:
			t.Fatal("no delivery during a continuous stream of events")
		case <-time.After(200 * time.Millisecond):
			write(t, root, "a.go", strings.Repeat("x", i+1))
		}
	}
}

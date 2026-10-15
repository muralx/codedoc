package daemon

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestFileEditDeliveredWithinASecond(t *testing.T) {
	root := newRepo(t, "a.go", "pkg/b.go")
	c := startRepo(t, root)
	began := time.Now()
	write(t, root, "pkg/b.go", "changed\n")
	c.waitPath("pkg/b.go")
	if d := time.Since(began); d > latencyLimit {
		t.Fatalf("delivered after %v", d)
	}
	got := c.snapshot()[0]
	if got.Workspace != "w" || got.Repo != "r" || got.HeadMoved {
		t.Fatalf("change: %+v", got)
	}
}

func TestBurstCoalesced(t *testing.T) {
	root := newRepo(t, "a.go")
	c := startRepo(t, root)
	for i := range 50 {
		write(t, root, "f"+strconv.Itoa(i)+".go", "x\n")
	}
	c.waitFor("all 50 files", func(cs []Change) bool {
		for i := range 50 {
			if !hasPath(cs, "f"+strconv.Itoa(i)+".go") {
				return false
			}
		}
		return true
	})
	if n := len(c.settle()); n > 3 {
		t.Fatalf("50 writes delivered as %d changes", n)
	}
}

func TestNewDirectoryDeliveredAndWatched(t *testing.T) {
	root := newRepo(t, "a.go")
	c := startRepo(t, root)
	write(t, root, "fresh/deep/x.go", "x\n")
	c.waitPath("fresh/deep/x.go")
	if !c.w.watched(filepath.Join(root, "fresh", "deep")) {
		t.Fatal("new directory not watched")
	}
	write(t, root, "fresh/deep/y.go", "y\n")
	c.waitPath("fresh/deep/y.go")
}

func TestDeletedFileDelivered(t *testing.T) {
	root := newRepo(t, "a.go", "gone.go")
	c := startRepo(t, root)
	if err := os.Remove(filepath.Join(root, "gone.go")); err != nil {
		t.Fatal(err)
	}
	c.waitPath("gone.go")
}

func TestRemovedDirectoryUnwatched(t *testing.T) {
	root := newRepo(t, "a.go", "sub/x.go")
	c := startRepo(t, root)
	if err := os.RemoveAll(filepath.Join(root, "sub")); err != nil {
		t.Fatal(err)
	}
	c.waitPath("sub")
	if c.w.watched(filepath.Join(root, "sub")) {
		t.Fatal("removed directory still watched")
	}
}

func TestIgnoredAndExcludedDirectoriesNotWatched(t *testing.T) {
	root := newRepo(t, "a.go", ".gitignore", "node_modules/m.js", "gen/g.go")
	write(t, root, ".gitignore", "out/\n*.log\n")
	write(t, root, "out/keep.txt", "untracked\n")
	c := startRepo(t, root, "**/gen/**")
	for _, d := range []string{"out", "node_modules", "gen", ".git"} {
		if c.w.watched(filepath.Join(root, d)) {
			t.Fatalf("%s is watched", d)
		}
	}
	write(t, root, "out/new.txt", "x\n")
	write(t, root, "debug.log", "x\n")
	write(t, root, "gen/h.go", "x\n")
	write(t, root, "a.go", "edited\n")
	c.waitPath("a.go")
	for _, p := range []string{"out/new.txt", "debug.log", "gen/h.go"} {
		if hasPath(c.settle(), p) {
			t.Fatalf("%s delivered", p)
		}
	}
}

func TestCreatedIgnoredDirectoryNotWatched(t *testing.T) {
	root := newRepo(t, "a.go", ".gitignore")
	write(t, root, ".gitignore", "tmp/\n")
	c := startRepo(t, root)
	write(t, root, "tmp/x.txt", "x\n")
	write(t, root, "a.go", "edited\n")
	c.waitPath("a.go")
	if c.w.watched(filepath.Join(root, "tmp")) || hasPath(c.settle(), "tmp") {
		t.Fatal("ignored directory watched or delivered")
	}
}

func TestNonGitDirectoryWatched(t *testing.T) {
	root := t.TempDir()
	write(t, root, "sub/a.txt", "x\n")
	c := startRepo(t, root)
	write(t, root, "sub/a.txt", "y\n")
	c.waitPath("sub/a.txt")
}

func TestNestedRepoWatchedOnlyByItself(t *testing.T) {
	outer := newRepo(t, "a.go")
	inner := filepath.Join(outer, "inner")
	write(t, outer, "inner/b.go", "x\n")
	c := start(t, []RepoSpec{
		{Workspace: "w", Name: "outer", Root: outer},
		{Workspace: "w", Name: "inner", Root: inner},
	}, io.Discard, nil)
	write(t, inner, "b.go", "y\n")
	c.waitFor("inner change", func(cs []Change) bool {
		return slices.ContainsFunc(cs, func(ch Change) bool { return ch.Repo == "inner" && slices.Contains(ch.Paths, "b.go") })
	})
	for _, ch := range c.settle() {
		if ch.Repo == "outer" {
			t.Fatalf("outer got %+v", ch)
		}
	}
}

func TestPanicInDeliverIsolated(t *testing.T) {
	root := newRepo(t, "a.go", "b.go")
	var calls atomic.Int32
	log := &lockedBuffer{}
	c := start(t, []RepoSpec{{Workspace: "w", Name: "r", Root: root}}, log, func(context.Context, Change) {
		if calls.Add(1) == 1 {
			panic("boom")
		}
	})
	write(t, root, "a.go", "edited\n")
	c.waitPath("a.go")
	write(t, root, "b.go", "edited\n")
	c.waitPath("b.go")
	// The collector records a change before calling deliver, so the
	// second call may still be on its way.
	eventually(t, "a second deliver call", func() bool { return calls.Load() >= 2 })
	if !log.contains("w/r: panic handling change: boom") {
		t.Fatalf("log: %s", log.String())
	}
}

func TestDefaults(t *testing.T) {
	o := withDefaults(WatchOptions{})
	if o.Debounce != 300*time.Millisecond || o.HeadInterval != 2*time.Second || o.Log == nil {
		t.Fatalf("defaults: %+v", o)
	}
}

func TestRunReportsClosedEventStream(t *testing.T) {
	w, err := NewWatcher([]RepoSpec{{Workspace: "w", Name: "r", Root: t.TempDir()}}, WatchOptions{Log: quietLogger()})
	if err != nil {
		t.Fatal(err)
	}
	_ = w.fs.Close()
	if err := w.Run(testCtx(t, waitLimit), func(context.Context, Change) {}); err == nil {
		t.Fatal("expected error from closed stream")
	}
}

func TestGitIgnoredReportsGitFailure(t *testing.T) {
	if _, err := gitIgnored(t.Context(), filepath.Join(t.TempDir(), "missing"), []string{"a"}); err == nil {
		t.Fatal("expected error")
	}
}

// TestBuildOutputWatchedOnlyAwayFromManifests checks the watcher shares
// discovery's built-in exclusions: testdata never, build output at the
// root or next to a manifest never, a package named build always.
func TestBuildOutputWatchedOnlyAwayFromManifests(t *testing.T) {
	root := newRepo(t, "go.mod", "a.go", "build/o.go", "pkg/testdata/t.go", "internal/build/b.go",
		"web/package.json", "web/dist/d.js")
	c := startRepo(t, root)
	for d, want := range map[string]bool{"build": false, "pkg/testdata": false, "web/dist": false,
		"internal/build": true, "web": true} {
		if got := c.w.watched(filepath.Join(root, filepath.FromSlash(d))); got != want {
			t.Errorf("%s watched %v, want %v", d, got, want)
		}
	}
	write(t, root, "internal/build/b.go", "edited\n")
	c.waitPath("internal/build/b.go")
}

// A manifest or lock file is reported even when an exclude glob or
// .gitignore keeps it out of the index, so dependency discovery runs.
func TestManifestChangesAreFlaggedWhateverTheExclusions(t *testing.T) {
	root := newRepo(t, "a.go", "yarn.lock", "sub/pnpm.lock")
	c := startRepo(t, root, "yarn.lock")
	write(t, root, "yarn.lock", "changed\n")
	c.waitFor("the excluded lock file", func(cs []Change) bool {
		return len(cs) > 0 && cs[len(cs)-1].Manifests && !hasPath(cs, "yarn.lock")
	})
	seen := len(c.settle())
	write(t, root, "sub/pnpm.lock", "changed\n")
	c.waitPath("sub/pnpm.lock")
	if got := c.snapshot()[seen]; !got.Manifests {
		t.Fatalf("an indexed lock file is flagged too: %+v", got)
	}
	seen = len(c.settle())
	write(t, root, "a.go", "package a\n")
	c.waitPath("a.go")
	if got := c.snapshot()[seen]; got.Manifests {
		t.Fatalf("an ordinary file is no manifest: %+v", got)
	}
}

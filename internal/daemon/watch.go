// Package daemon keeps workspace indexes fresh in the background: it
// watches repo working trees, debounces their changes, notices branch
// switches, and hands each repo's batch of changed paths to a callback.
package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Change is a batch of repo-relative paths that changed in one repo. A
// path may name a directory that was removed or renamed away, meaning
// everything below it.
type Change struct {
	Workspace, Repo string
	// Paths are slash-separated, relative to the repo root, sorted.
	Paths []string
	// HeadMoved reports a branch switch or new HEAD commit.
	HeadMoved bool
	// Rescan asks for a full freshness pass because events may have been
	// lost (a watch error, an event overflow, a root that came back);
	// after it succeeds, call Watcher.Recovered with the change.
	Rescan bool
	// Manifests reports that a file WatchOptions.Manifest accepts
	// changed: a dependency manifest or lock file. It is set even when
	// the file itself is excluded or gitignored and so not in Paths.
	Manifests bool
	// seq is the repo's degradation count when the change was taken.
	seq uint64
}

// RepoSpec names one repo to watch.
type RepoSpec struct {
	Workspace, Name string
	// Root is the absolute repo directory.
	Root string
	// Exclude holds the workspace exclude globs.
	Exclude []string
}

// WatchOptions tune a Watcher; zero values take the defaults.
type WatchOptions struct {
	// Debounce is the quiet period before a repo's changes are
	// delivered; default 300 ms.
	Debounce time.Duration
	// HeadInterval is how often .git/HEAD is polled; default 2 s.
	HeadInterval time.Duration
	// Log receives errors and recovered panics; default stderr only.
	Log *Logger
	// Manifest, when set, tells the repo-relative paths whose change is
	// reported as Change.Manifests whatever the exclusions say.
	Manifest func(rel string) bool
}

// Watcher watches repos and delivers debounced Changes.
type Watcher struct {
	opts  WatchOptions
	log   *Logger
	fs    *fsnotify.Watcher
	repos []*repoState
	roots map[string]bool

	mu   sync.Mutex
	dirs map[string]*repoState // watched directory → its repo
}

// NewWatcher starts watching every eligible directory of the repos:
// all except built-in exclusions, exclude globs, gitignored directories
// and the roots of other listed repos (which watch themselves). A repo
// whose root is missing, or some of whose directories cannot be
// watched, is logged and degraded (see Degraded) instead of failing the
// others; a missing root is watched once it appears.
func NewWatcher(repos []RepoSpec, opts WatchOptions) (*Watcher, error) {
	opts = withDefaults(opts)
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &Watcher{opts: opts, log: opts.Log, fs: fw, roots: map[string]bool{}, dirs: map[string]*repoState{}}
	for _, spec := range repos {
		if spec.Root, err = filepath.Abs(spec.Root); err != nil {
			_ = fw.Close()
			return nil, err
		}
		w.roots[spec.Root] = true
		w.repos = append(w.repos, newRepoState(spec, opts.Debounce))
	}
	for _, r := range w.repos {
		if info, err := os.Stat(r.spec.Root); err != nil || !info.IsDir() {
			w.log.Printf("%s: watch %s: not a directory", r.label(), r.spec.Root)
			r.missing = true
			w.degrade(r, "root missing: "+r.spec.Root)
			continue
		}
		w.watchTree(context.Background(), r, r.spec.Root)
	}
	return w, nil
}

func withDefaults(o WatchOptions) WatchOptions {
	if o.Debounce <= 0 {
		o.Debounce = 300 * time.Millisecond
	}
	if o.HeadInterval <= 0 {
		o.HeadInterval = 2 * time.Second
	}
	if o.Log == nil {
		o.Log = &Logger{stderr: os.Stderr, max: maxLogSize}
	}
	return o
}

// Run delivers changes until ctx ends, then stops watching and returns
// once in-flight deliveries finish. deliver receives a context that ends
// with ctx and must return promptly once it does, or Run cannot return.
// Each repo has its own goroutine, so deliver may block one repo without
// stalling the others; a panic in deliver is logged and the repo keeps
// being watched.
func (w *Watcher) Run(ctx context.Context, deliver func(context.Context, Change)) error {
	defer func() { _ = w.fs.Close() }()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	for _, r := range w.repos {
		wg.Go(func() { w.runRepo(ctx, r, deliver) })
	}
	wg.Go(func() { w.pollHeads(ctx) })
	wg.Go(func() { w.drainErrors(ctx) })
	err := w.loop(ctx)
	cancel()
	wg.Wait()
	return err
}

// loop dispatches filesystem events until ctx ends. It may call
// fsnotify Add and Remove, so it must not be the reader of fsnotify's
// Errors: see drainErrors.
func (w *Watcher) loop(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-w.fs.Events:
			if !ok {
				return errors.New("daemon: fsnotify event stream closed")
			}
			w.handle(ctx, ev)
		}
	}
}

// drainErrors logs fsnotify errors until ctx ends or the stream closes.
// fsnotify sends some errors while holding the lock its Add and Remove
// take, so they are read here, apart from the event loop that calls
// Add and Remove, and without taking any Watcher lock.
func (w *Watcher) drainErrors(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case err, ok := <-w.fs.Errors:
			if !ok {
				return
			}
			w.watchError(err)
		}
	}
}

// handle records one event against the repo of its directory. A created
// directory is watched and its files are recorded with it. An excluded
// path is dropped, unless it is a manifest (see excludedManifest).
func (w *Watcher) handle(ctx context.Context, ev fsnotify.Event) {
	r := w.repoOf(filepath.Dir(ev.Name))
	if r == nil {
		return
	}
	rel := r.rel(ev.Name)
	info, err := os.Lstat(ev.Name)
	isDir := err == nil && info.IsDir()
	manifest := w.isManifest(rel, isDir)
	if r.excl.Path(rel, isDir) {
		w.excludedManifest(r, manifest)
		return
	}
	if (ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename)) && w.watched(ev.Name) {
		w.unwatch(ev.Name)
	}
	if !isDir || !ev.Has(fsnotify.Create) {
		r.add(w.opts.Debounce, change{paths: []string{rel}, manifests: manifest})
		return
	}
	if ignored, _ := gitIgnored(ctx, r.spec.Root, []string{rel}); ignored[rel] || w.roots[ev.Name] {
		return
	}
	w.watchTree(ctx, r, ev.Name)
	r.add(w.opts.Debounce, change{paths: append(w.filesBelow(r, ev.Name), rel)})
}

// isManifest reports whether a path is a file WatchOptions.Manifest
// accepts.
func (w *Watcher) isManifest(rel string, isDir bool) bool {
	return !isDir && w.opts.Manifest != nil && w.opts.Manifest(rel)
}

// excludedManifest records the change of an excluded file when it is a
// manifest: its path is not delivered, only the fact (Change.Manifests).
func (w *Watcher) excludedManifest(r *repoState, manifest bool) {
	if manifest {
		r.add(w.opts.Debounce, change{manifests: true})
	}
}

// pollHeads checks every repo each HeadInterval: a root that went away
// or came back (see checkRoot), and a HEAD state that differs, which is
// recorded as a HeadMoved change.
func (w *Watcher) pollHeads(ctx context.Context) {
	t := time.NewTicker(w.opts.HeadInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		for _, r := range w.repos {
			if w.checkRoot(ctx, r); w.Missing(r.spec.Workspace, r.spec.Name) {
				continue
			}
			if s := headState(r.spec.Root); s != r.head {
				r.head = s
				r.add(w.opts.Debounce, change{headMoved: true})
			}
		}
	}
}

func (w *Watcher) repoOf(dir string) *repoState {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.dirs[dir]
}

func (w *Watcher) watched(dir string) bool { return w.repoOf(dir) != nil }

func (w *Watcher) setDir(dir string, r *repoState) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.dirs[dir] = r
}

// unwatch forgets a removed or renamed directory and everything below
// it, then drops their watches. fsnotify is called without holding
// w.mu, and its errors are ignored: the kernel or fsnotify itself may
// already have dropped a watch. The event loop calls this between
// events, so a directory re-created at the same path is re-added only
// after the removal is done.
func (w *Watcher) unwatch(dir string) {
	var gone []string
	w.mu.Lock()
	prefix := dir + string(filepath.Separator)
	for d := range w.dirs {
		if d == dir || strings.HasPrefix(d, prefix) {
			delete(w.dirs, d)
			gone = append(gone, d)
		}
	}
	w.mu.Unlock()
	for _, d := range gone {
		_ = w.fs.Remove(d)
	}
}

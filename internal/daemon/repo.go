package daemon

import (
	"context"
	"runtime/debug"
	"sort"
	"sync"
	"time"

	"github.com/muralx/codedoc/internal/exclude"
)

// maxWait bounds how long a stream of events can postpone a delivery:
// the debounce restarts on every event, but never past maxWait after
// the first pending one.
const maxWait = 2 * time.Second

// repoState collects the pending changes of one repo and delivers them,
// debounced, from the repo's own goroutine.
type repoState struct {
	spec RepoSpec
	// excl decides which of the repo's paths are not watched.
	excl exclude.Matcher
	head string // last HEAD state seen; touched only by the poller

	mu        sync.Mutex
	paths     map[string]bool
	headMoved bool
	rescan    bool
	manifests bool
	first     time.Time // when the oldest pending change arrived
	inflight  bool      // a taken change is being delivered
	timer     *time.Timer
	ready     chan struct{} // capacity 1: a flush is due
	degraded  string        // why changes may be missed; "" when watched in full
	seq       uint64        // counts degradations, see Watcher.Recovered
	missing   bool          // the root directory is gone
}

// change is what one event adds to a repo's pending change.
type change struct {
	headMoved, rescan, manifests bool
	paths                        []string
}

func newRepoState(spec RepoSpec, debounce time.Duration) *repoState {
	r := &repoState{spec: spec, excl: exclude.New(spec.Root, spec.Exclude), paths: map[string]bool{},
		ready: make(chan struct{}, 1)}
	r.timer = time.AfterFunc(debounce, r.signal)
	r.timer.Stop()
	r.head = headState(spec.Root)
	return r
}

func (r *repoState) label() string { return r.spec.Workspace + "/" + r.spec.Name }

func (r *repoState) signal() {
	select {
	case r.ready <- struct{}{}:
	default:
	}
}

// add records changed paths, HEAD movement or a rescan and restarts the
// debounce, at most until maxWait after the first pending change.
func (r *repoState) add(debounce time.Duration, c change) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range c.paths {
		r.paths[p] = true
	}
	r.headMoved = r.headMoved || c.headMoved
	r.rescan = r.rescan || c.rescan
	r.manifests = r.manifests || c.manifests
	now := time.Now()
	if r.first.IsZero() {
		r.first = now
	}
	r.timer.Reset(max(min(debounce, maxWait-now.Sub(r.first)), 0))
}

// take returns and clears the pending change; ok is false when nothing
// is pending.
func (r *repoState) take() (c Change, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.paths) == 0 && !r.headMoved && !r.rescan && !r.manifests {
		return Change{}, false
	}
	c = Change{Workspace: r.spec.Workspace, Repo: r.spec.Name, HeadMoved: r.headMoved, Rescan: r.rescan,
		Manifests: r.manifests, seq: r.seq}
	for p := range r.paths {
		c.Paths = append(c.Paths, p)
	}
	sort.Strings(c.Paths)
	r.paths, r.headMoved, r.rescan, r.manifests, r.inflight, r.first = map[string]bool{}, false, false, false, true, time.Time{}
	return c, true
}

// done marks the change last taken as handled.
func (r *repoState) done() {
	r.mu.Lock()
	r.inflight = false
	r.mu.Unlock()
}

// pending counts the paths waiting for delivery, plus one for a HEAD
// move and one for a change being delivered.
func (r *repoState) pending() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := len(r.paths)
	if r.headMoved || r.rescan || r.manifests {
		n++
	}
	if r.inflight {
		n++
	}
	return n
}

// Pending reports the work not yet handled for a repo: changed paths
// waiting out the debounce or queued behind a delivery, a HEAD move or
// rescan, and one for a delivery in progress. It is 0 for an unknown
// repo.
func (w *Watcher) Pending(workspace, repo string) int {
	if r := w.repoNamed(workspace, repo); r != nil {
		return r.pending()
	}
	return 0
}

// run delivers pending changes until ctx ends. Calls are serial for the
// repo, so a slow deliver coalesces the events that arrive meanwhile.
func (w *Watcher) runRepo(ctx context.Context, r *repoState, deliver func(context.Context, Change)) {
	for {
		select {
		case <-ctx.Done():
			r.timer.Stop()
			return
		case <-r.ready:
		}
		c, ok := r.take()
		if !ok {
			continue
		}
		if c.Rescan && !w.Missing(c.Workspace, c.Repo) {
			w.watchTree(ctx, r, r.spec.Root)
		}
		if c.Paths = w.dropIgnored(ctx, r, c.Paths); ctx.Err() == nil &&
			(len(c.Paths) > 0 || c.HeadMoved || c.Rescan || c.Manifests) {
			w.safeDeliver(ctx, r, c, deliver)
		}
		r.done()
	}
}

// safeDeliver calls deliver, logging a panic with the repo name instead
// of letting it stop the daemon.
func (w *Watcher) safeDeliver(ctx context.Context, r *repoState, c Change, deliver func(context.Context, Change)) {
	defer func() {
		if v := recover(); v != nil {
			w.log.Printf("%s: panic handling change: %v\n%s", r.label(), v, debug.Stack())
		}
	}()
	deliver(ctx, c)
}

// dropIgnored removes gitignored paths from a batch.
func (w *Watcher) dropIgnored(ctx context.Context, r *repoState, paths []string) []string {
	ignored, err := gitIgnored(ctx, r.spec.Root, paths)
	if err != nil {
		w.log.Printf("%s: check-ignore: %v", r.label(), err)
	}
	kept := paths[:0]
	for _, p := range paths {
		if !ignored[p] {
			kept = append(kept, p)
		}
	}
	if len(kept) == 0 {
		return nil
	}
	return kept
}

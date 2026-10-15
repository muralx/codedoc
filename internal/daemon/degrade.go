package daemon

import (
	"context"
	"errors"
	"os"
	"syscall"

	"github.com/fsnotify/fsnotify"
)

// enospcHint is the degraded reason of a repo that ran out of inotify
// watches.
const enospcHint = "out of inotify watches (raise fs.inotify.max_user_watches, e.g. " +
	"`sudo sysctl fs.inotify.max_user_watches=524288`, and restart the daemon)"

// Degraded returns why the changes of a repo may be missed ("" when it
// is watched in full): a watch that could not be added, a watch error or
// event overflow not yet followed by a rescan, or a missing root. A
// degraded repo is not reported fresh, so queries run their own
// freshness check. It is "" for an unknown repo.
func (w *Watcher) Degraded(workspace, repo string) string {
	if r := w.repoNamed(workspace, repo); r != nil {
		return r.degradedReason()
	}
	return ""
}

// Missing reports whether a repo's root directory is gone.
func (w *Watcher) Missing(workspace, repo string) bool {
	r := w.repoNamed(workspace, repo)
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.missing
}

// Recovered clears the degraded state of the repo of c, a rescan that
// was indexed in full, unless the repo degraded again since c was taken.
func (w *Watcher) Recovered(c Change) {
	r := w.repoNamed(c.Workspace, c.Repo)
	if r == nil || !c.Rescan {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.seq == c.seq && !r.missing {
		r.degraded = ""
	}
}

func (w *Watcher) repoNamed(workspace, repo string) *repoState {
	for _, r := range w.repos {
		if r.spec.Workspace == workspace && r.spec.Name == repo {
			return r
		}
	}
	return nil
}

// degrade marks r degraded for reason, logging it once per new reason.
func (w *Watcher) degrade(r *repoState, reason string) {
	r.mu.Lock()
	changed := r.degraded != reason
	r.degraded = reason
	r.seq++
	r.mu.Unlock()
	if changed {
		w.log.Printf("%s: degraded: %s", r.label(), reason)
	}
}

func (r *repoState) degradedReason() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.degraded
}

// watchFailed handles a watch that could not be added to dir: the repo
// is degraded and keeps being served. It reports whether walking the
// tree should stop, which it should once inotify ran out of watches.
func (w *Watcher) watchFailed(r *repoState, dir string, err error) (stop bool) {
	w.log.Printf("%s: watch %s: %v", r.label(), dir, err)
	if errors.Is(err, syscall.ENOSPC) {
		w.degrade(r, enospcHint)
		return true
	}
	w.degrade(r, "cannot watch "+dir+": "+err.Error())
	return false
}

// watchError handles an fsnotify error: every repo may have missed
// events, so each is degraded and rescanned (re-watched and indexed in
// full); the rescan clears the degraded state.
func (w *Watcher) watchError(err error) {
	w.log.Printf("watch error: %v", err)
	reason := "watch error: " + err.Error()
	if errors.Is(err, fsnotify.ErrEventOverflow) {
		reason = "event queue overflow; rescanning"
	}
	for _, r := range w.repos {
		w.degrade(r, reason)
		r.add(w.opts.Debounce, change{rescan: true})
	}
}

// checkRoot follows the presence of a repo root: a root that went away
// degrades the repo and drops its watches; one that came back is watched
// again and rescanned.
func (w *Watcher) checkRoot(ctx context.Context, r *repoState) {
	info, err := os.Stat(r.spec.Root)
	present := err == nil && info.IsDir()
	r.mu.Lock()
	was := !r.missing
	r.missing = !present
	r.mu.Unlock()
	switch {
	case was && !present:
		w.degrade(r, "root missing: "+r.spec.Root)
		w.unwatch(r.spec.Root)
	case !was && present:
		w.log.Printf("%s: root %s is back; watching it again", r.label(), r.spec.Root)
		w.degrade(r, "root reappeared; rescanning")
		w.watchTree(ctx, r, r.spec.Root)
		r.add(w.opts.Debounce, change{rescan: true})
	}
}

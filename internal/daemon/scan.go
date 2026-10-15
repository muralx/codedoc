package daemon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// gitTimeout bounds one git subprocess; git check-ignore normally
// answers in milliseconds.
const gitTimeout = 30 * time.Second

// gitIgnored returns the subset of repo-relative paths that git ignores,
// asking `git check-ignore` once for the whole batch. A root that is not
// a git working tree ignores nothing. git is killed when ctx ends or
// after gitTimeout, and an error is returned.
func gitIgnored(ctx context.Context, root string, rels []string) (map[string]bool, error) {
	ignored := map[string]bool{}
	if len(rels) == 0 {
		return ignored, nil
	}
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", root, "check-ignore", "--stdin", "-z")
	// Children of git that keep its pipes open must not stall Wait.
	cmd.WaitDelay = time.Second
	cmd.Stdin = strings.NewReader(strings.Join(rels, "\x00") + "\x00")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	var exit *exec.ExitError
	switch {
	case err == nil:
	case ctx.Err() != nil:
		return ignored, fmt.Errorf("git check-ignore: %w", ctx.Err())
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		return ignored, nil
	case errors.As(err, &exit) && strings.Contains(stderr.String(), "not a git repository"):
		return ignored, nil
	default:
		return ignored, errors.Join(err, errors.New(strings.TrimSpace(stderr.String())))
	}
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			ignored[p] = true
		}
	}
	return ignored, nil
}

// subdirs lists the child directories of dir that may be watched: not
// excluded, not the root of another watched repo, and not gitignored.
func (w *Watcher) subdirs(ctx context.Context, r *repoState, dirs []string) []string {
	var cands, rels []string
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			full := filepath.Join(dir, e.Name())
			rel := r.rel(full)
			if !e.IsDir() || r.excl.Path(rel, true) || w.roots[full] {
				continue
			}
			cands, rels = append(cands, full), append(rels, rel)
		}
	}
	ignored, err := gitIgnored(ctx, r.spec.Root, rels)
	if err != nil {
		w.log.Printf("%s: check-ignore: %v", r.label(), err)
	}
	kept := cands[:0]
	for i, c := range cands {
		if !ignored[rels[i]] {
			kept = append(kept, c)
		}
	}
	return kept
}

// watchTree watches dir and every eligible directory below it, one
// directory level per check-ignore call. dir itself must be eligible.
// A directory that cannot be watched degrades the repo (see
// watchFailed); running out of inotify watches stops the walk. It stops
// early once ctx ends.
func (w *Watcher) watchTree(ctx context.Context, r *repoState, dir string) {
	level := []string{dir}
	for len(level) > 0 && ctx.Err() == nil {
		var ok []string
		for _, d := range level {
			err := w.fs.Add(d)
			switch {
			case err == nil:
				w.setDir(d, r)
				ok = append(ok, d)
			case errors.Is(err, fs.ErrNotExist):
			case w.watchFailed(r, d, err):
				return
			}
		}
		level = w.subdirs(ctx, r, ok)
	}
}

// filesBelow lists the repo-relative paths of regular files under dir
// that lie in watched directories, so the files of a directory created
// after startup are delivered with it.
func (w *Watcher) filesBelow(r *repoState, dir string) []string {
	var out []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return nil
		case d.IsDir() && !w.watched(p):
			return filepath.SkipDir
		case d.Type().IsRegular():
			if rel := r.rel(p); !r.excl.Path(rel, false) {
				out = append(out, rel)
			}
		}
		return nil
	})
	return out
}

// rel returns the slash-separated path of p relative to the repo root.
// p must be the root or lie below it.
func (r *repoState) rel(p string) string {
	rel := strings.TrimPrefix(p, r.spec.Root+string(filepath.Separator))
	if p == r.spec.Root {
		rel = "."
	}
	return path.Clean(filepath.ToSlash(rel))
}

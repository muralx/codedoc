package query

import (
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/muralx/codedoc/internal/buildinfo"
	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/feedback"
	"github.com/muralx/codedoc/internal/gitmeta"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/stablekey"
)

// FeedbackOpts name what a feedback report is about.
type FeedbackOpts struct {
	Key  string // the stable key the answer was about
	Path string // the file, as outline takes it
	Cmd  string // the command that gave the answer
}

// FeedbackEntry describes a report an agent files when an answer and
// the file disagree (documents spec section 4.2): the time, the
// workspace, the repo with its head and dirty flag, the codedoc build
// and extractor version, the text and the options, and, when Key or
// Path names an indexed file, the content hash the index holds for it
// and the one of the file on disk. The repo is the one of Key or Path,
// else the current one. The index is read as it is: a report describes
// the state that gave the answer, so nothing is re-indexed.
func FeedbackEntry(c *Ctx, text string, o FeedbackOpts) feedback.Entry {
	e := feedback.Entry{Time: c.now().UTC().Format(time.RFC3339), Workspace: c.Workspace.Name,
		Build: buildinfo.Version, Extractor: index.ExtractorVersion(), Text: text, Key: o.Key, Path: o.Path, Cmd: o.Cmd}
	r, p := c.feedbackFile(o)
	if r == nil {
		return e
	}
	e.Repo = r.Name
	if head, err := gitmeta.Head(r.Path); err == nil {
		e.Head = output.HeadRef(head.Branch, head.Commit)
		dirty, _ := gitmeta.DirtyFiles(r.Path)
		e.Dirty = len(dirty) > 0
	}
	if p == "" {
		return e
	}
	if id, err := c.repoID(r); err == nil {
		if f, err := c.Store.FileByPath(id, p); err == nil {
			e.IndexedHash = f.Hash
		}
	}
	if src, err := os.ReadFile(filepath.Join(r.Path, filepath.FromSlash(p))); err == nil {
		e.CurrentHash = index.ContentHash(src)
	}
	return e
}

// feedbackFile returns the repo a report is about and the repo-relative
// path of its file ("" when it names none): those of Path, else of the
// symbol Key names, else the current repo.
func (c *Ctx) feedbackFile(o FeedbackOpts) (*config.Repo, string) {
	if o.Path != "" {
		if r, p, err := c.splitTarget(o.Path); err == nil {
			return r, p
		}
	}
	if o.Key != "" {
		r, err := c.repoNamed(stablekey.Repo(o.Key))
		if err != nil {
			return c.Repo, ""
		}
		sy, err := c.Store.SymbolByKey(o.Key)
		if err != nil {
			return r, "" // the report may be about a key the index lacks
		}
		return r, sy.Path
	}
	return c.Repo, ""
}

// TimeBound resolves a time written as a duration back from now (30m,
// 3h, 2d, 1w) or as a date (2026-10-09, optionally with a time, in the
// local zone); ok is false for anything else.
func TimeBound(v string, now time.Time) (at time.Time, ok bool) {
	if m := lsDuration.FindStringSubmatch(v); m != nil {
		n, _ := strconv.Atoi(m[1]) // lsDuration guarantees digits
		unit := map[string]time.Duration{"m": time.Minute, "h": time.Hour, "d": 24 * time.Hour, "w": 7 * 24 * time.Hour}[m[2]]
		return now.Add(-time.Duration(n) * unit), true
	}
	for _, layout := range dateLayouts {
		if t, err := time.ParseInLocation(layout, v, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

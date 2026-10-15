package query

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/gitmeta"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/store"
)

// History lists the commits that touched a symbol's lines, its doc
// included, newest first, at most limit of them (0: all). git log -L
// follows lines of HEAD, so the symbol is first found at HEAD (its file
// there extracted and matched by qualified name and signature hash):
// uncommitted edits above it do not shift the range. A symbol of an
// untracked file, or one that does not exist at HEAD yet, has none.
func History(c *Ctx, addr string, limit int) (*output.Response, error) {
	sy, resp, err := c.one(addr)
	if err != nil {
		return resp, err
	}
	r, err := c.repoOf(sy)
	if err != nil {
		return nil, err
	}
	title := about("history of ", sy)
	tracked, err := gitmeta.IsTracked(r.Path, sy.Path)
	if errors.Is(err, gitmeta.ErrNotRepo) || (err == nil && !tracked) {
		c.notes = append(c.notes, sy.Path+" has no committed history")
		return c.respond([]*config.Repo{r}, c.titled(title, nil)), nil
	}
	if err != nil {
		return nil, err
	}
	head, err := symbolAt(r, sy, "HEAD")
	if err != nil {
		return nil, err
	}
	if !head.found {
		c.notes = append(c.notes, sy.StableKey+" does not exist at HEAD; its history starts in the working tree")
		return c.respond([]*config.Repo{r}, c.titled(title, nil)), nil
	}
	commits, err := gitmeta.LogRange(r.Path, sy.Path, head.start, head.end, limit)
	if err != nil {
		return nil, err
	}
	entries := make([]output.Entry, len(commits))
	for i, cm := range commits {
		entries[i] = output.Entry{Key: cm.Commit[:min(7, len(cm.Commit))],
			Text: time.Unix(cm.At, 0).UTC().Format("2006-01-02") + " " + cm.Author, Doc: cm.Subject,
			Extra: map[string]any{"commit": cm.Commit, "at": cm.At, "author": cm.Author, "subject": cm.Subject}}
	}
	return c.respond([]*config.Repo{r}, c.titled(title, entries)), nil
}

// AtOpts select the revisions of an at answer.
type AtOpts struct {
	// Ref is the revision the symbol is shown at.
	Ref string
	// Diff, when set, is the revision to diff against; WorkTree for the
	// working tree.
	Diff string
}

// WorkTree names the working tree as a diff side.
const WorkTree = "worktree"

// At shows a symbol as it was at a revision, found by extracting its
// file there and matching its qualified name (a file that moved is not
// found), with its doc and line numbers; with Diff, a unified diff of
// the symbol's text between Ref and Diff.
func At(c *Ctx, addr string, o AtOpts) (*output.Response, error) {
	sy, resp, err := c.one(addr)
	if err != nil {
		return resp, err
	}
	r, err := c.repoOf(sy)
	if err != nil {
		return nil, err
	}
	repos := []*config.Repo{r}
	a, err := symbolAt(r, sy, o.Ref)
	if err != nil || !a.found {
		return notFoundAt(sy, o.Ref, err)
	}
	if o.Diff == "" {
		e := symbolEntry(sy)
		e.Loc, e.Body, e.BodyStart = fmt.Sprintf("%s:%d@%s", sy.Path, a.start, o.Ref), a.text, a.start
		return c.respond(repos, output.Section{Title: "at " + o.Ref, Entries: []output.Entry{e}}), nil
	}
	b, err := symbolAt(r, sy, revOf(o.Diff))
	if err != nil || !b.found {
		return notFoundAt(sy, o.Diff, err)
	}
	d := unifiedDiff(o.Ref+":"+sy.Path, o.Diff+":"+sy.Path, lines(a.text), lines(b.text), a.start, b.start)
	e := symbolEntry(sy)
	e.Body, e.Plain = d, true
	if d == "" {
		c.notes = append(c.notes, fmt.Sprintf("no changes between %s and %s", o.Ref, o.Diff))
	}
	return c.respond(repos, output.Section{Title: "diff " + o.Ref + ".." + o.Diff, Entries: []output.Entry{e}}), nil
}

// revOf maps the WorkTree diff side to the empty revision.
func revOf(rev string) string {
	if rev == WorkTree {
		return ""
	}
	return rev
}

func lines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

// notFoundAt is the one line saying that a symbol is not at a revision
// and how to find where it was, or the error that kept it from being
// looked up.
func notFoundAt(sy store.Symbol, rev string, err error) (*output.Response, error) {
	if err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("symbol %s at %s: %w: the file or the symbol may have moved; codedoc history %s lists its commits",
		shortName(sy), rev, ErrNotFound, shellQuote(sy.StableKey))
}

// symbolText is a symbol's text at a revision with its first and last
// line.
type symbolText struct {
	text       string
	start, end int
	found      bool
}

// symbolAt extracts a symbol's file at rev ("" for the working tree) and
// returns the symbol's lines, its doc included.
func symbolAt(r *config.Repo, sy store.Symbol, rev string) (symbolText, error) {
	src, ok, err := sourceAt(r, sy.Path, rev)
	if err != nil || !ok {
		return symbolText{}, err
	}
	t := symbolTarget(r.Path, sy, store.File{})
	ex, ok := extract.For(sy.Lang)
	if !ok {
		return symbolText{}, fmt.Errorf("no extractor for %s", sy.Lang)
	}
	res, err := ex.Extract(extractPathOf(t), src)
	if err != nil {
		return symbolText{}, err
	}
	for _, s := range res.Symbols {
		if s.Qualified == t.Qualified && (t.SigHash == "" || s.SigHash == t.SigHash) {
			from := s.Span.StartLine
			if s.Doc != nil && s.Doc.Span.StartLine < from {
				from = s.Doc.Span.StartLine
			}
			all := strings.SplitAfter(string(src), "\n")
			end := min(s.Span.EndLine, len(all))
			return symbolText{text: strings.Join(all[from-1:end], ""), start: from, end: end, found: true}, nil
		}
	}
	return symbolText{}, nil
}

// sourceAt reads a repo file at rev ("" for the working tree); ok is
// false when it does not exist there.
func sourceAt(r *config.Repo, p, rev string) ([]byte, bool, error) {
	var src []byte
	var err error
	if rev == "" {
		src, err = os.ReadFile(filepath.Join(r.Path, filepath.FromSlash(p)))
	} else {
		src, err = gitmeta.Show(r.Path, rev, p)
	}
	if errors.Is(err, gitmeta.ErrNoPath) || errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	return src, err == nil, err
}

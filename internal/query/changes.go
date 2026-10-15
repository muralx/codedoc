package query

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/docs/write"
	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/gitmeta"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/parse"
	"github.com/muralx/codedoc/internal/store"
)

// Changes lists, per repo in scope, the files that differ between a ref
// or duration (12h, 3d, 2w: the newest commit older than that) and the
// working tree, uncommitted and untracked files included, renames
// detected, and the symbols in them tagged with their delta since the
// ref (added, removed, moved, body-changed, doc-changed, doc-added,
// doc-removed) and their current doc state; the sections of changed
// documents are listed too (kind section, tagged added, removed, moved
// or text-changed, without a doc state). The ref side comes from
// extracting the file's content at the ref (read through one git
// cat-file process per repo). Symbols match by qualified name in their
// file (its old path when renamed); the ones left over match across
// the changeset (see matchMoves) and are tagged moved. Repos the ref
// names no commit in are skipped with a note naming them; a ref no
// repo in scope has is an error.
func Changes(c *Ctx, since string) (*output.Response, error) {
	repos := c.scope()
	if err := c.EnsureFresh(repos...); err != nil {
		return nil, err
	}
	var files, syms []output.Entry
	var lacking []string
	var unknown error
	for _, r := range repos {
		ref, err := gitmeta.ResolveSince(r.Path, since)
		switch {
		case errors.Is(err, gitmeta.ErrNotRepo):
			c.notes = append(c.notes, r.Name+" is not a git repository")
			continue
		case errors.Is(err, gitmeta.ErrUnknownRef):
			lacking, unknown = append(lacking, r.Name), err
			continue
		case err != nil:
			return nil, err
		}
		fe, se, err := c.repoChanges(r, ref)
		if err != nil {
			return nil, err
		}
		files, syms = append(files, fe...), append(syms, se...)
	}
	if len(lacking) > 0 && len(lacking) == len(repos) {
		return nil, unknown
	}
	if len(lacking) > 0 {
		c.notes = append(c.notes, fmt.Sprintf("%q not found in %s; skipped", since, strings.Join(lacking, ", ")))
	}
	return c.respond(repos, c.tagged("changed files since "+since, files),
		c.tagged("changed symbols", syms)), nil
}

// repoChanges maps the files of one repo changed since ref (a full
// hash) to symbol deltas.
func (c *Ctx) repoChanges(r *config.Repo, ref string) (files, syms []output.Entry, err error) {
	changed, err := gitmeta.ChangesSince(r.Path, ref)
	if err != nil || len(changed) == 0 {
		return nil, nil, err
	}
	id, err := c.repoID(r)
	if err != nil {
		return nil, nil, err
	}
	blobs, err := gitmeta.OpenBlobs(r.Path, ref)
	if err != nil {
		return nil, nil, err
	}
	defer func() { err = errors.Join(err, blobs.Close()) }()
	fcs := make([]*fileChange, len(changed))
	for i, ch := range changed {
		if fcs[i], err = c.fileChanges(r, id, blobs, ch); err != nil {
			return nil, nil, err
		}
	}
	matchMoves(fcs)
	short := ref[:min(7, len(ref))]
	for _, fc := range fcs {
		deltas := fc.deltas(r.Name, short)
		files = append(files, fc.entry(r.Name, len(deltas)))
		syms = append(syms, deltas...)
	}
	return files, syms, nil
}

// fileChange is what changed in one file: its current symbols and the
// ones its content at the ref declared, paired by matchInFile and
// matchMoves.
type fileChange struct {
	ch            gitmeta.Changed
	before, after bool // the file exists at the ref, in the working tree
	cur           []store.Symbol
	prior         []extract.Symbol
	priorPath     string // the path the ref content was extracted under
	// pair maps a current symbol to its version at the ref; moved marks
	// the pairs found in another file, and taken the prior symbols paired.
	pair  map[int]priorRef
	taken map[int]bool
}

// priorRef points at a symbol of a file at the ref.
type priorRef struct {
	fc    *fileChange
	idx   int
	moved bool
}

func (fc *fileChange) entry(repo string, n int) output.Entry {
	status := "modified"
	switch {
	case fc.ch.Status == 'R':
		status = "renamed from " + fc.ch.OldPath
	case !fc.before:
		status = "added"
	case !fc.after:
		status = "deleted"
	}
	text := fmt.Sprintf("%s, %d symbol %s", status, n, plural(n, "change", "changes"))
	return output.Entry{Loc: repo + ":" + fc.ch.Path, Text: text, Extra: map[string]any{"status": status,
		"symbol_changes": n}}
}

// fileChanges reads a changed file at the ref (its old path when
// renamed) and loads its indexed symbols.
func (c *Ctx) fileChanges(r *config.Repo, repoID int64, blobs *gitmeta.Blobs, ch gitmeta.Changed) (*fileChange, error) {
	fc := &fileChange{ch: ch, pair: map[int]priorRef{}, taken: map[int]bool{}}
	_, err := os.Stat(filepath.Join(r.Path, filepath.FromSlash(ch.Path)))
	fc.after = err == nil
	oldPath := ch.Path
	if ch.OldPath != "" {
		oldPath = ch.OldPath
	}
	old, ok, err := blobs.Read(oldPath)
	if err != nil {
		return nil, err
	}
	fc.before = ok
	if fc.cur, err = c.inFileID(repoID, ch.Path); err != nil {
		return nil, err
	}
	ex, found := extractorFor(oldPath)
	if !found || !fc.before {
		matchInFile(fc)
		return fc, nil
	}
	fc.priorPath = refExtractPath(oldPath, ch.Path, fc.cur)
	if res, err := ex.Extract(fc.priorPath, old); err == nil {
		fc.prior = res.Symbols
	}
	matchInFile(fc)
	return fc, nil
}

// inFileID returns the indexed symbols of a file, none when it is not
// indexed.
func (c *Ctx) inFileID(repoID int64, p string) ([]store.Symbol, error) {
	if repoID == 0 {
		return nil, nil
	}
	f, err := c.Store.FileByPath(repoID, p)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return c.Store.SymbolsInFile(f.ID)
}

// refExtractPath is the path a file that is now at p is extracted under
// at the ref, where it was at old: for TypeScript, old relative to the
// package root its indexed qualified names start from, else old itself.
func refExtractPath(old, p string, cur []store.Symbol) string {
	for _, sy := range cur {
		if parse.Family(sy.Lang) == "ts" {
			rel := extractPathOf(symbolTarget("", sy, store.File{}))
			if root, ok := strings.CutSuffix(p, rel); ok && strings.HasPrefix(old, root) {
				return old[len(root):]
			}
			return old
		}
	}
	return old
}

// extractPathOf is the path the extractor sees for a target.
func extractPathOf(t write.Target) string {
	if t.ExtractPath != "" {
		return t.ExtractPath
	}
	return t.Path
}

// deltas tags the current symbols of a file against their versions at
// the ref and lists the symbols the file held at the ref that went
// nowhere.
func (fc *fileChange) deltas(repo, ref string) []output.Entry {
	var out []output.Entry
	for i, sy := range fc.cur {
		p, had := fc.pair[i]
		tags := fc.tags(sy, p, had)
		if len(tags) == 0 {
			continue
		}
		e := symbolEntry(sy)
		e.Tags = append(e.Tags, tags...)
		if !isDocSymbol(sy) {
			e.Tags = append(e.Tags, docTag(sy.DocState))
		}
		if had && p.moved {
			e.Extra["from"] = fmt.Sprintf("%s:%d", p.fc.oldPath(), p.fc.prior[p.idx].Span.StartLine)
		}
		out = append(out, e)
	}
	for i, s := range fc.prior {
		if !fc.taken[i] {
			out = append(out, output.Entry{Loc: fmt.Sprintf("%s:%s:%d@%s", repo, fc.oldPath(), s.Span.StartLine, ref),
				Text: s.SigText, Tags: []string{"removed"}, Extra: map[string]any{"qualified": s.Qualified}})
		}
	}
	return out
}

// tags are the delta tags of a current symbol against its version at
// the ref, p when had. A section is tagged added or text-changed, with
// no doc state.
func (fc *fileChange) tags(sy store.Symbol, p priorRef, had bool) []string {
	var prior extract.Symbol
	if had {
		prior = p.fc.prior[p.idx]
	}
	tags := deltaTags(sy, prior, had)
	if isDocSymbol(sy) {
		tags = docDeltaTags(sy, prior, had)
	}
	if had && p.moved {
		tags = append([]string{"moved"}, tags...)
	}
	return tags
}

// oldPath is the file's path at the ref.
func (fc *fileChange) oldPath() string {
	if fc.ch.OldPath != "" {
		return fc.ch.OldPath
	}
	return fc.ch.Path
}

// deltaTags compares a current symbol with its version at the ref.
func deltaTags(sy store.Symbol, s extract.Symbol, had bool) []string {
	if !had {
		return []string{"added"}
	}
	var tags []string
	if s.BodyHash != sy.BodyHash {
		tags = append(tags, "body-changed")
	}
	old := ""
	if s.Doc != nil {
		old = s.Doc.Text
	}
	switch {
	case old == "" && sy.DocText != "":
		tags = append(tags, "doc-added")
	case old != "" && sy.DocText == "":
		tags = append(tags, "doc-removed")
	case strings.Join(strings.Fields(old), " ") != strings.Join(strings.Fields(sy.DocText), " "):
		tags = append(tags, "doc-changed")
	}
	return tags
}

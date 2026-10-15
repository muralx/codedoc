package query

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/parse"
	"github.com/muralx/codedoc/internal/store"
)

// OutlineOpts order a directory outline; a file outline is always in
// line order.
type OutlineOpts struct {
	// Sort is name (the default), mtime, committed, size or lines, as
	// for ls (see LsOpts.Sort); files and subdirectories are each sorted
	// among themselves.
	Sort string
	// Reverse inverts the order.
	Reverse bool
	// Doc prints the whole doc of each symbol of a file outline in place
	// of its first sentence.
	Doc bool
}

// check validates the sort key and applies the default.
func (o *OutlineOpts) check() error {
	if o.Sort == "" {
		o.Sort = "name"
	}
	if !slices.Contains(LsSorts, o.Sort) {
		return fmt.Errorf("unknown --sort %q; valid: %s", o.Sort, strings.Join(LsSorts, ", "))
	}
	return nil
}

// Outline lists the symbols of a file, or the files and subdirectories of
// a directory or module. target is repo:path, or a path relative to the
// current repo; without a current repo a leading repo name segment is
// accepted too (go-lib/store). A target that is no such path but a module
// identity (github.com/acme/x/store, @acme/domain) outlines the module's
// directory. A target the workspace does not have may name a dependency
// (see depOutline): a package lists its modules, a module or a file of a
// package its symbols. An unknown path is answered for the path it may
// mean, or says why the file is outside the index (see missingPath).
func Outline(c *Ctx, target string, o OutlineOpts) (*output.Response, error) {
	if err := o.check(); err != nil {
		return nil, err
	}
	return c.outline(target, o)
}

// outline is Outline with checked options.
func (c *Ctx) outline(target string, o OutlineOpts) (*output.Response, error) {
	r, p, err := c.splitTarget(target)
	if err != nil {
		if resp, ok, oerr := c.elseOutline(target, o); ok || oerr != nil {
			return resp, oerr
		}
		return nil, err
	}
	repos := []*config.Repo{r}
	if err := c.EnsureFresh(r); err != nil {
		return nil, err
	}
	id, err := c.repoID(r)
	if err != nil {
		return nil, err
	}
	f, err := c.Store.FileByPath(id, p)
	switch {
	case err == nil:
		sec, err := c.fileOutline(f, o)
		return c.respond(repos, sec), err
	case !errors.Is(err, store.ErrNotFound):
		return nil, err
	}
	mark := len(c.notes)
	sec, found, err := c.dirOutline(id, p, o)
	if err != nil || found {
		return c.respond(repos, sec), err
	}
	// The workspace has no such path: what that attempt noted (an offset
	// past the end of nothing) says nothing about the answer that follows.
	c.notes = c.notes[:mark]
	if resp, ok, err := c.elseOutline(target, o); ok || err != nil {
		return resp, err
	}
	return c.missingPath("outline", target, r, p, true, func(addr string) (*output.Response, error) {
		return c.outline(addr, o)
	})
}

// elseOutline outlines a target that is no path of the workspace: a
// module identity, then a dependency (see depOutline). The notes of an
// attempt that found nothing are discarded.
func (c *Ctx) elseOutline(target string, o OutlineOpts) (*output.Response, bool, error) {
	mark := len(c.notes)
	if resp, ok, err := c.moduleOutline(target, o); ok || err != nil {
		return resp, ok, err
	}
	c.notes = c.notes[:mark]
	resp, ok, err := c.depOutline(target)
	if !ok && err == nil {
		c.notes = c.notes[:mark]
	}
	return resp, ok, err
}

// moduleOutline outlines the directory of the one module whose identity
// is target; ok is false when no single module has it.
func (c *Ctx) moduleOutline(target string, o OutlineOpts) (*output.Response, bool, error) {
	mods, err := c.Store.ModulesByIdentity(strings.TrimSuffix(target, "/"))
	if err != nil || len(mods) != 1 {
		return nil, false, err
	}
	row, err := c.Store.RepoByID(mods[0].RepoID)
	if err != nil {
		return nil, false, err
	}
	r, err := c.repoNamed(row.Name)
	if err != nil {
		return nil, false, err
	}
	if err := c.EnsureFresh(r); err != nil {
		return nil, false, err
	}
	sec, _, err := c.dirOutline(row.ID, mods[0].Path, o)
	sec.Title = mods[0].Identity + " (" + r.Name + ":" + mods[0].Path + ")"
	return c.respond([]*config.Repo{r}, sec), true, err
}

// splitTarget resolves a path argument to a repo and a clean
// repo-relative path ("." for the root), see locate. A path that climbs
// out of its repo, or leads out of what queries may read through a
// symbolic link, is refused with ErrOutside (see contained): every
// command that takes a path resolves it here.
func (c *Ctx) splitTarget(target string) (*config.Repo, string, error) {
	r, p, err := c.locate(target)
	if err != nil {
		return nil, "", err
	}
	if escapes(p) || !c.contained(filepath.Join(r.Path, filepath.FromSlash(p))) {
		return nil, "", outside(target)
	}
	return r, p, nil
}

// locate resolves an outline target to a repo and a clean repo-relative
// path. repo:path names the repo. A plain path is tried relative to the
// working directory first (unless --repo chose the repo), then relative
// to the current repo's root, then as a repo name followed by a path. An
// absolute path that lies in no repo is an error: ErrOutside unless it
// lies in a directory of a dependency.
func (c *Ctx) locate(target string) (*config.Repo, string, error) {
	if repo, p, ok := strings.Cut(target, ":"); ok {
		r, err := c.repoNamed(repo)
		return r, path.Clean("./" + p), err
	}
	if r, p, ok := c.cwdTarget(target); ok {
		return r, p, nil
	}
	if filepath.IsAbs(target) && !c.Filter {
		if !c.contained(target) {
			return nil, "", outside(target)
		}
		return nil, "", fmt.Errorf("outline %s: not inside a repo", target)
	}
	p := path.Clean("./" + filepath.ToSlash(target))
	if c.Repo != nil {
		return c.Repo, p, nil
	}
	first, rest, _ := strings.Cut(p, "/")
	if r, err := c.repoNamed(first); err == nil {
		return r, path.Clean("./" + rest), nil
	}
	return nil, "", fmt.Errorf("outline %s: not inside a repo; write repo:path", target)
}

// cwdTarget resolves target against the working directory: ok when that
// path exists and lies in a workspace repo (the innermost one).
func (c *Ctx) cwdTarget(target string) (*config.Repo, string, bool) {
	if c.Filter || (c.Cwd == "" && !filepath.IsAbs(target)) {
		return nil, "", false
	}
	abs := target
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(c.Cwd, target)
	}
	if _, err := os.Stat(abs); err != nil {
		return nil, "", false
	}
	var best *config.Repo
	rel := ""
	for _, r := range c.Workspace.Repos {
		if p, ok := relWithin(r.Path, abs); ok && (best == nil || len(r.Path) > len(best.Path)) {
			best, rel = r, p
		}
	}
	return best, rel, best != nil
}

// relWithin returns abs relative to root as a clean slash path, ok when
// abs is root or below it.
func relWithin(root, abs string) (string, bool) {
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return path.Clean(filepath.ToSlash(rel)), true
}

// fileOutline lists a file's symbols in line order. Members inside their
// container's span collapse into a "+N members" tag on the nearest listed
// ancestor, so grandchildren count too; members declared outside it (Go
// methods) are listed. A document lists its heading tree (see
// docOutline).
func (c *Ctx) fileOutline(f store.File, o OutlineOpts) (output.Section, error) {
	if parse.IsDoc(f.Lang) {
		return c.docOutline(f)
	}
	syms, err := c.Store.SymbolsInFile(f.ID)
	if err != nil {
		return output.Section{}, err
	}
	listed, members := fold(syms, true)
	entries := make([]output.Entry, len(listed))
	for i, sy := range listed {
		entries[i] = outlineEntry(sy, members[sy.ID])
		if o.Doc {
			entries[i].Doc = wholeDoc(sy.DocText)
		}
	}
	if err := c.detailed(entries, listed); err != nil {
		return output.Section{}, err
	}
	sec := c.section(fmt.Sprintf("%s, %d %s", f.Path, f.Lines, plural(f.Lines, "line", "lines")), entries)
	sec.Path = f.Path
	return sec, nil
}

// wholeDoc is a doc on one line.
func wholeDoc(doc string) string { return strings.Join(strings.Fields(doc), " ") }

// fold splits the symbols of an outline into the ones it lists and the
// number of members folded into each: a symbol is folded into the
// topmost container reached by climbing from it (see listedAncestor).
// The outlines of the workspace and of a dependency both fold here.
func fold(syms []store.Symbol, inSpan bool) (listed []store.Symbol, members map[int64]int) {
	byID := make(map[int64]store.Symbol, len(syms))
	for _, sy := range syms {
		byID[sy.ID] = sy
	}
	members = map[int64]int{}
	for _, sy := range syms {
		if top := listedAncestor(byID, sy, inSpan); top != sy.ID {
			members[top]++
			continue
		}
		listed = append(listed, sy)
	}
	return listed, members
}

// listedAncestor climbs from sy through its containers and returns the
// id of the topmost one: sy itself when it is listed. With inSpan the
// climb stops at a container whose span does not hold the symbol below,
// so a member declared outside its container (a Go method) is listed.
func listedAncestor(byID map[int64]store.Symbol, sy store.Symbol, inSpan bool) int64 {
	for range len(byID) { // bounded: a container cycle cannot loop forever
		p, ok := byID[sy.ContainerID]
		if !ok || inSpan && (p.StartLine > sy.StartLine || sy.EndLine > p.EndLine) {
			break
		}
		sy = p
	}
	return sy.ID
}

// outlineEntry is an outline line of a workspace symbol: its span, its
// signature, its doc state when the doc is missing or stale and its
// member count.
func outlineEntry(sy store.Symbol, members int) output.Entry {
	e := symbolEntry(sy)
	e.Tags = append(e.Tags, docTags(sy)...)
	return outlined(e, sy, members)
}

// outlined turns the entry of a symbol into its outline line: the span
// in place of the line, the signature as it is written (the kind is a
// tag of --detail, see detailed) and a tag counting the members folded
// into it. Workspace and dependency outlines both render here.
func outlined(e output.Entry, sy store.Symbol, members int) output.Entry {
	e.Loc = spanLoc(sy.Path, sy.StartLine, sy.EndLine)
	if members > 0 {
		e.Tags = append(e.Tags, fmt.Sprintf("+%d %s", members, plural(members, "member", "members")))
	}
	return e
}

// outlineItem is one line of a directory outline with what it sorts by.
type outlineItem struct {
	lsKey
	files int // of a subdirectory
	text  string
}

// dirOutline lists the files directly in a directory with symbol and line
// counts, then its subdirectories with file counts, each with its
// modification time (a subdirectory's is its newest file's), ordered as
// the outline options say. found is false when no indexed file lies
// under the directory.
func (c *Ctx) dirOutline(repoID int64, dir string, o OutlineOpts) (output.Section, bool, error) {
	prefix := dir + "/"
	if dir == "." {
		prefix = ""
	}
	files, err := c.Store.SymbolCountsByFile(repoID, prefix)
	if err != nil {
		return output.Section{}, false, err
	}
	var own, subs []*outlineItem
	bySub := map[string]*outlineItem{}
	for _, f := range files {
		k := lsKey{name: f.Path, mtime: f.Mtime, committed: f.LastCommitAt, size: f.Size, lines: f.Lines}
		sub, _, nested := strings.Cut(f.Path[len(prefix):], "/")
		if !nested {
			own = append(own, &outlineItem{lsKey: k, text: fmt.Sprintf("%d %s, %d %s", f.Symbols,
				plural(f.Symbols, "symbol", "symbols"), f.Lines, plural(f.Lines, "line", "lines"))})
			continue
		}
		d := bySub[sub]
		if d == nil {
			d = &outlineItem{lsKey: lsKey{name: prefix + sub + "/"}}
			bySub[sub], subs = d, append(subs, d)
		}
		d.files++
		d.size, d.lines = d.size+k.size, d.lines+k.lines
		d.mtime, d.committed = max(d.mtime, k.mtime), max(d.committed, k.committed)
	}
	for _, d := range subs {
		d.text = fmt.Sprintf("%d files", d.files)
	}
	entries := append(outlineEntries(own, o), outlineEntries(subs, o)...)
	return c.section(dir, entries), len(entries) > 0, nil
}

// outlineEntries sorts the lines of a directory outline and renders
// them: path, counts, modification time.
func outlineEntries(items []*outlineItem, o OutlineOpts) []output.Entry {
	sortBy(items, LsOpts{Sort: o.Sort, Reverse: o.Reverse}, func(it *outlineItem) lsKey { return it.lsKey })
	out := make([]output.Entry, len(items))
	for i, it := range items {
		out[i] = output.Entry{Loc: it.name, Text: it.text + "  " + stamp(it.mtime/int64(time.Second)),
			Extra: map[string]any{"mtime": it.mtime / int64(time.Second), "lines": it.lines, "size": it.size}}
	}
	return out
}

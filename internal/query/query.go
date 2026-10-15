// Package query answers structural questions about an indexed workspace
// and returns them as output responses.
package query

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/embed"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/store"
)

// ErrNotFound reports an address nothing could be offered for, and
// ErrAmbiguous one that matched more than one symbol when resolution is
// strict (spec 13.1: exit status 1, and 3 under --strict).
var (
	ErrNotFound  = errors.New("not found")
	ErrAmbiguous = errors.New("ambiguous")
)

// Ctx carries everything a query needs.
type Ctx struct {
	Store     *store.Store
	Workspace *config.Workspace
	// Repo is the current repo: the one named by --repo, else the one
	// containing the working directory. It may be nil.
	Repo *config.Repo
	// Filter is true when Repo was chosen with --repo; searches are then
	// limited to it.
	Filter bool
	// Cwd is the working directory relative paths are resolved from
	// first; empty skips that step.
	Cwd string
	// Offset is the number of ranked entries to skip in each section.
	Offset int
	// Budget is the output budget in bytes the answer is rendered under,
	// 0 for unlimited. Queries whose sections multiply (a config glob)
	// use it to build only what can be shown.
	Budget int
	// Fresh holds the freshness check result per repo name, filled by
	// EnsureFresh.
	Fresh map[string]index.Freshness
	// Embedder embeds search queries and the vectors freshness checks
	// re-extract; nil makes search text-only.
	Embedder embed.Embedder
	// DaemonFresh, when set, asks the daemon whether a repo of a
	// workspace is fresh; a true answer skips the stat pass of the
	// freshness check (spec 7.8). The repo is passed whole so the daemon
	// can check it keeps that directory current.
	DaemonFresh func(workspace string, r *config.Repo) bool
	// DepsDB is the path of the shared dependency database (deps spec
	// section 2); when set, EnsureFresh also keeps the dependency uses of
	// the repos current. Empty leaves dependencies alone.
	DepsDB string
	// DepsPending counts the package versions of the repos EnsureFresh
	// checked that are not indexed yet: the N of "deps: N pending".
	DepsPending int
	// Deps includes the third-party packages the workspace depends on in
	// find, outline, symbol, search and explain (--deps); without it a
	// name reaches them when no workspace symbol has it, and an address
	// that names a dependency always does (deps spec section 7).
	Deps bool
	// Strict makes an address that names nothing an error (ErrNotFound)
	// and one that names several symbols another (ErrAmbiguous, with the
	// candidates), where a query otherwise answers for the nearest or
	// the best candidate (--strict, efficiency spec section 4).
	Strict bool
	// exact is how the call being made resolves addresses (see
	// exactness).
	exact exactness
	// depsChecked and depsRepos hold the repos whose dependencies this
	// context already checked.
	depsChecked map[string]bool
	depsRepos   []*config.Repo
	// view is the dependency view, opened on first use (see deps);
	// viewTried tells that opening it was tried, usedDeps that the answer
	// being built looked at dependencies.
	view                *depView
	viewTried, usedDeps bool
	// Keys prints stable keys in place of names in text (--keys); Detail
	// every tag, signature and doc text hides by default (--detail).
	Keys, Detail bool
	// Context is how many lines of the file are shown around each call
	// site (-C, -A, -B, --context symbol|file); the zero value shows the
	// line of the call alone.
	Context Context
	// files caches fileLines by file row.
	files map[int64][]string
	// Command is the command line of the query without --offset, quoted
	// for a shell: omission lines build the exact follow-up from it.
	Command string
	// Scope holds the flags that choose the workspace and the repo, as
	// they follow a command (" --workspace w"), "" when none was given:
	// an answer that names other commands for the rest of its sections
	// ends them with it.
	Scope string
	// Now dates the recency of gaps; nil means time.Now.
	Now func() time.Time
	// notes collects lines for the response note while sections are built.
	notes []string
	// roots caches readRoots.
	roots []string
	// names caches repoNames.
	names map[int64]string
	// draws holds the repo ids of the symbols and modules a query
	// started from: the answer draws on them besides its entries.
	draws []int64
}

// scope returns the repos a search covers: the --repo repo, or all.
func (c *Ctx) scope() []*config.Repo {
	if c.Filter && c.Repo != nil {
		return []*config.Repo{c.Repo}
	}
	return c.Workspace.Repos
}

// repoNamed returns the workspace repo with a name.
func (c *Ctx) repoNamed(name string) (*config.Repo, error) {
	for _, r := range c.Workspace.Repos {
		if r.Name == name {
			return r, nil
		}
	}
	return nil, fmt.Errorf("workspace %s has no repo named %q", c.Workspace.Name, name)
}

// repoID returns the row id of a repo, 0 when it was never indexed.
func (c *Ctx) repoID(r *config.Repo) (int64, error) {
	row, err := c.Store.RepoByPath(r.Path)
	if errors.Is(err, store.ErrNotFound) {
		return 0, nil
	}
	return row.ID, err
}

// filterID is the repo id searches are limited to, 0 for the workspace.
func (c *Ctx) filterID() (int64, error) {
	if !c.Filter || c.Repo == nil {
		return 0, nil
	}
	id, err := c.repoID(c.Repo)
	if id == 0 && err == nil {
		id = -1 // never indexed: match nothing
	}
	return id, err
}

// respond builds a response whose header describes the repos the answer
// used. An answer that draws on one repo (see drawnOn), or only on the
// --repo repo, gets that repo's header and unprefixed locations; any
// other gets a workspace header listing the repos it draws on in config
// order (all of repos when it draws on none), and its locations are
// prefixed with their repo name. Dirty counts and freshness cover repos,
// the repos the answer was computed from; the stale ones are named. An
// answer that looked at dependencies while package versions wait to be
// indexed says so: "deps: N pending" (deps spec section 9).
func (c *Ctx) respond(repos []*config.Repo, sections ...output.Section) *output.Response {
	var h output.Header
	if one := c.singleRepo(repos, sections); one != nil {
		f := c.Fresh[one.Name]
		h = output.Header{Repo: one.Name, Branch: f.Branch, Commit: f.Commit}
	} else {
		h = output.Header{Workspace: c.Workspace.Name}
		h.Repos, h.Main = c.drawnOn(repos, sections)
	}
	h.Fresh = true
	for _, r := range repos {
		f := c.Fresh[r.Name]
		if h.Workspace != "" || r.Name == h.Repo {
			h.Dirty += f.Dirty
		}
		if !f.Fresh {
			h.Fresh = false
			h.Stale = append(h.Stale, r.Name)
		}
	}
	c.draws = nil
	notes := append(c.notes, c.staleNotes(repos)...)
	if c.usedDeps && c.DepsPending > 0 {
		notes = append(notes, fmt.Sprintf("deps: %d pending", c.DepsPending))
	}
	c.notes, c.usedDeps = nil, false
	h.Multi = len(c.Workspace.Repos) > 1
	return &output.Response{Header: h, Sections: sections, Note: strings.Join(notes, ". "), Keys: c.Keys,
		Detail: c.Detail, Command: c.Command}
}

// singleRepo returns the repo a one-repo header describes: the only one
// of repos, the --repo repo when the answer draws on no other, or the
// one repo the answer draws on; nil for a workspace-wide answer.
func (c *Ctx) singleRepo(repos []*config.Repo, sections []output.Section) *config.Repo {
	if len(repos) == 1 {
		return repos[0]
	}
	count := c.drawCounts(sections)
	if c.Filter && c.Repo != nil && (len(count) == 0 || len(count) == 1 && count[c.Repo.Name] > 0) {
		return c.Repo
	}
	if len(count) != 1 {
		return nil
	}
	for _, r := range repos {
		if count[r.Name] > 0 {
			return r
		}
	}
	return nil
}

// drawCounts counts, per repo name, the symbol or module the answer
// started from and the entries of sections (by repo id, else see
// entryRepo).
func (c *Ctx) drawCounts(sections []output.Section) map[string]int {
	names := c.repoNames()
	count := map[string]int{}
	for _, id := range c.draws {
		if names[id] != "" {
			count[names[id]]++
		}
	}
	for _, s := range sections {
		for _, e := range s.Entries {
			if name := c.entryRepo(e); name != "" {
				count[name]++
			}
		}
	}
	return count
}

// drawnOn returns the heads of the repos of repos the answer draws on
// (see drawCounts), in config order, and the name of the one most of it
// draws on, prefixing the location of each entry with a repo id with the
// repo name. With nothing drawn on it returns the heads of all of repos.
func (c *Ctx) drawnOn(repos []*config.Repo, sections []output.Section) ([]output.RepoHead, string) {
	count := c.drawCounts(sections)
	names := c.repoNames()
	for i := range sections {
		for j := range sections[i].Entries {
			if e := &sections[i].Entries[j]; names[e.RepoID] != "" && e.Loc != "" {
				e.Loc = names[e.RepoID] + ":" + e.Loc
			}
		}
	}
	var heads []output.RepoHead
	main := ""
	for _, r := range repos {
		if len(count) == 0 || count[r.Name] > 0 {
			heads = append(heads, c.head(r))
			if main == "" || count[r.Name] > count[main] {
				main = r.Name
			}
		}
	}
	return heads, main
}

// head describes a repo in a workspace header.
func (c *Ctx) head(r *config.Repo) output.RepoHead {
	f := c.Fresh[r.Name]
	return output.RepoHead{Name: r.Name, Branch: f.Branch, Commit: f.Commit, Dirty: f.Dirty, Fresh: f.Fresh}
}

// entryRepo names the repo of an entry: by its repo id, else the repo its
// location starts with (repo:path), else the repo its key starts with (a
// stable key, a module key repo:dir/ or a file key repo:path), else "".
func (c *Ctx) entryRepo(e output.Entry) string {
	if name := c.repoNames()[e.RepoID]; name != "" {
		return name
	}
	for _, s := range []string{e.Loc, e.Key} {
		if repo, _, ok := strings.Cut(s, ":"); ok {
			if _, err := c.repoNamed(repo); err == nil {
				return repo
			}
		}
	}
	return ""
}

// repoNames maps the row id of every indexed repo of the workspace to
// its name, read once per context.
func (c *Ctx) repoNames() map[int64]string {
	if c.names != nil {
		return c.names
	}
	c.names = map[int64]string{}
	for _, r := range c.Workspace.Repos {
		if id, err := c.repoID(r); err == nil && id != 0 {
			c.names[id] = r.Name
		}
	}
	return c.names
}

// ancestry returns, for a tree section whose first entry after an offset
// is nested (its parents were skipped), the keys of its ancestors from
// the top (see entryName) joined by " > "; "" when nothing is orphaned.
func ancestry(entries []output.Entry, off int) string {
	if off == 0 || off >= len(entries) || entries[off].Depth == 0 {
		return ""
	}
	var path []string
	depth := entries[off].Depth
	for i := off - 1; i >= 0 && depth > 0; i-- {
		if entries[i].Depth == depth-1 {
			path = append([]string{entryName(entries[i])}, path...)
			depth--
		}
	}
	return strings.Join(path, " > ")
}

// entryName names an entry in a note: its name, else its key, else the
// heading of a document section (the entries of a heading tree carry
// neither), else its location.
func entryName(e output.Entry) string {
	if e.Name != "" {
		return e.Name
	}
	if e.Key != "" {
		return e.Key
	}
	if h, ok := e.Extra["heading"].(string); ok && h != "" {
		return h
	}
	return e.Loc
}

// plural returns one when n is 1, else many.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// notePastEnd notes an offset that skips every entry of a section of
// total entries, once per response: a multi-section answer names the
// largest section.
func (c *Ctx) notePastEnd(total int) {
	prefix := fmt.Sprintf("offset %d is past the end", c.Offset)
	for i, n := range c.notes {
		if strings.HasPrefix(n, prefix) {
			var prev int
			if _, err := fmt.Sscanf(n[len(prefix):], " (total %d)", &prev); err == nil && total > prev {
				c.notes[i] = fmt.Sprintf("%s (total %d)", prefix, total)
			}
			return
		}
	}
	c.notes = append(c.notes, fmt.Sprintf("%s (total %d)", prefix, total))
}

// section builds a section from ranked entries, skipping c.Offset; an
// offset that skips them all is noted (see notePastEnd).
func (c *Ctx) section(title string, entries []output.Entry) output.Section {
	if c.Offset > 0 && c.Offset >= len(entries) {
		c.notePastEnd(len(entries))
	}
	off := min(c.Offset, len(entries))
	if path := ancestry(entries, off); path != "" {
		c.notes = append(c.notes, fmt.Sprintf("%s continues under %s", title, path))
	}
	return output.Section{Title: title, Entries: entries[off:], Offset: off}
}

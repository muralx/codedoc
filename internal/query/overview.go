package query

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/docs"
	"github.com/muralx/codedoc/internal/gitmeta"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/store"
)

// Overview limits.
const (
	maxHeadings      = 12
	maxRecent        = 10
	moduleTreeDepth  = 2
	headingLevels    = 2
	commitHashLength = 7
)

// Overview orients a session in the workspace or the --repo repo (spec
// 9): per repo its head, dirty count and size; the top-level headings of
// README.md and CLAUDE.md; entry points; doc coverage; the last
// commits; the packages it depends on, when it has any; and the module
// tree to depth 2 with one-line docs, file and line counts. The module
// tree comes last, so the budget cuts it first. A workspace header
// counts every repo overviewed (see allHeads).
func Overview(c *Ctx) (*output.Response, error) {
	repos := c.scope()
	if err := c.EnsureFresh(repos...); err != nil {
		return nil, err
	}
	var heads, headings, entries, commits, coverage, modules, packages []output.Entry
	for _, r := range repos {
		id, err := c.repoID(r)
		if err != nil {
			return nil, err
		}
		if id == 0 {
			continue
		}
		parts, err := c.repoOverview(r, id)
		if err != nil {
			return nil, err
		}
		heads, headings = append(heads, parts.head), append(headings, parts.headings...)
		entries, commits = append(entries, parts.entries...), append(commits, parts.commits...)
		coverage, modules = append(coverage, parts.coverage), append(modules, parts.modules...)
		packages = append(packages, parts.packages...)
	}
	secs := []output.Section{c.section("repos", heads), c.section("readme headings", headings),
		c.tagged("entry points", entries), c.section("doc coverage", coverage), c.section("recent commits", commits)}
	if len(packages) > 0 {
		secs = append(secs, c.section("dependencies", packages))
	}
	resp := c.respond(repos, append(secs, c.section("modules", modules))...)
	if resp.Header.Workspace != "" {
		resp.Header.Repos, resp.Header.Main = c.allHeads(repos)
	}
	return resp, nil
}

// allHeads returns the header heads of every repo of repos, and the
// name of the current repo among them, else of the first. The overview
// describes all of them, also a repo none of whose entries carries its
// name in a key (a repo of documents has no module and no entry point).
func (c *Ctx) allHeads(repos []*config.Repo) ([]output.RepoHead, string) {
	heads := make([]output.RepoHead, len(repos))
	main := ""
	for i, r := range repos {
		heads[i] = c.head(r)
		if main == "" || c.Repo != nil && r.Name == c.Repo.Name {
			main = r.Name
		}
	}
	return heads, main
}

// repoParts is the overview of one repo.
type repoParts struct {
	head, coverage                                output.Entry
	headings, entries, commits, modules, packages []output.Entry
}

func (c *Ctx) repoOverview(r *config.Repo, id int64) (repoParts, error) {
	var p repoParts
	files, syms, err := c.Store.RepoStats(id)
	if err != nil {
		return p, err
	}
	f := c.Fresh[r.Name]
	p.head = output.Entry{Key: r.Name, Text: fmt.Sprintf("%s dirty:%d files:%d symbols:%d",
		output.HeadRef(f.Branch, f.Commit), f.Dirty, files, syms), Extra: map[string]any{"path": r.Path}}
	p.headings = readmeHeadings(r)
	n, err := c.Store.RepoDocCounts(id)
	if err != nil {
		return p, err
	}
	p.coverage = countEntry(r.Name, n)
	if p.entries, err = c.entryPoints(r, id); err != nil {
		return p, err
	}
	if p.commits, err = recentCommits(r); err != nil {
		return p, err
	}
	if p.packages, err = c.overviewDeps(r, id); err != nil {
		return p, err
	}
	p.modules, err = c.overviewModules(r, id)
	return p, err
}

// readmeHeadings lists the level one and two headings of the repo's
// README.md and CLAUDE.md, the first maxHeadings of each.
func readmeHeadings(r *config.Repo) []output.Entry {
	var out []output.Entry
	for _, name := range []string{"README.md", "CLAUDE.md"} {
		b, err := os.ReadFile(filepath.Join(r.Path, name))
		if err != nil {
			continue
		}
		hs := docs.Headings(string(b), headingLevels)
		for _, h := range hs[:min(len(hs), maxHeadings)] {
			out = append(out, output.Entry{Loc: r.Name + ":" + name, Text: h})
		}
	}
	return out
}

// recentCommits lists the last commits of a repo; none outside git.
func recentCommits(r *config.Repo) ([]output.Entry, error) {
	commits, err := gitmeta.RecentCommits(r.Path, maxRecent)
	if errors.Is(err, gitmeta.ErrNotRepo) {
		return nil, nil
	}
	out := make([]output.Entry, len(commits))
	for i, cm := range commits {
		out[i] = output.Entry{Key: cm.Commit[:min(commitHashLength, len(cm.Commit))],
			Text: r.Name + " " + time.Unix(cm.At, 0).UTC().Format("2006-01-02") + " " + cm.Author, Doc: cm.Subject,
			Extra: map[string]any{"repo": r.Name, "commit": cm.Commit, "at": cm.At}}
	}
	return out, err
}

// overviewModules lists a repo's modules to moduleTreeDepth levels, parents
// before children, with one-line docs and file and line counts (see
// docs.ModuleSizes).
func (c *Ctx) overviewModules(r *config.Repo, id int64) ([]output.Entry, error) {
	mods, err := c.Store.ModulesByRepo(id)
	if err != nil {
		return nil, err
	}
	files, err := c.Store.FilesByRepo(id)
	if err != nil {
		return nil, err
	}
	sizes := docs.ModuleSizes(mods, files)
	children := map[int64][]store.Module{}
	known := map[int64]bool{}
	for _, m := range mods {
		known[m.ID] = true
	}
	for _, m := range mods {
		parent := m.ParentID
		if !known[parent] {
			parent = 0
		}
		children[parent] = append(children[parent], m)
	}
	var out []output.Entry
	var walk func(parent int64, depth int)
	walk = func(parent int64, depth int) {
		for _, m := range children[parent] {
			out = append(out, moduleTreeEntry(r, m, sizes[m.ID], depth))
			if depth+1 < moduleTreeDepth {
				walk(m.ID, depth+1)
			}
		}
	}
	walk(0, 0)
	return out, nil
}

// moduleTreeEntry renders one module of the tree with its size, which
// includes the modules nested under it (see docs.ModuleSizes).
func moduleTreeEntry(r *config.Repo, m store.Module, s docs.ModuleSize, depth int) output.Entry {
	e := output.Entry{Key: m.Identity, Loc: r.Name + ":" + m.Path, Depth: depth,
		Text: fmt.Sprintf("%s %s, %d %s, %d %s", m.Lang, m.Kind, s.Files, plural(s.Files, "file", "files"), s.Lines,
			plural(s.Lines, "line", "lines")),
		Doc: firstSentence(m.DocText), Extra: map[string]any{"doc_state": m.DocState}}
	if m.DocState != "ok" {
		e.Tags = []string{docTag(m.DocState)}
	}
	return e
}

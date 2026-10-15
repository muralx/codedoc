package query

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/parse"
	"github.com/muralx/codedoc/internal/store"
)

// explainTop is how many callers, callees or module symbols explain
// lists, ranked by fan-in.
const explainTop = 5

// Explain answers "what is this and where does it sit" for a symbol or a
// path. For a symbol it prints the doc stack: the workspace (its repos,
// each with the first sentence of its README), the repo (first paragraph
// of its README), the module chain from the repo's root module down to
// the symbol's module (each module's first doc paragraph), the
// container's doc, and the symbol's signature, full doc and doc state;
// then its top callers and callees by fan-in with one-line docs. A file
// or directory path (repo:path, a path in the current repo, or a module
// identity) explains the module or file instead: its stack, then its
// top-level symbols. A package of the workspace's dependencies (its name
// or id), one of its modules, or with Ctx.Deps one of its symbols is
// explained from deps.db: the README, the module doc, and what the
// workspace uses of it.
func Explain(c *Ctx, target string) (*output.Response, error) {
	if a := ParseAddr(target); a.Kind == AddrBare {
		if resp, ok, err := c.explainPath(target); ok || err != nil {
			return resp, err
		}
		if resp, ok, err := c.depExplainPackage(target); ok || err != nil {
			return resp, err
		}
	}
	t, resp, err := c.graphTargetIn(target, depsOnFlag)
	if err != nil {
		return resp, err
	}
	if t.dep != nil {
		return c.depExplain(*t.dep)
	}
	secs, err := c.symbolExplained(t.sym)
	if err != nil {
		return nil, err
	}
	return c.respond(c.Workspace.Repos, secs...), nil
}

// symbolExplained returns the sections that explain a workspace symbol:
// its doc stack, its top callers and its top callees.
func (c *Ctx) symbolExplained(sy store.Symbol) ([]output.Section, error) {
	stack, err := c.symbolStack(sy)
	if err != nil {
		return nil, err
	}
	callers, err := c.rankedNeighbours(sy.ID, true)
	if err != nil {
		return nil, err
	}
	callees, err := c.rankedNeighbours(sy.ID, false)
	if err != nil {
		return nil, err
	}
	return []output.Section{{Title: "explain " + shortName(sy), KeyTitle: "explain " + sy.StableKey, Entries: stack},
		c.section("top callers", callers), c.section("top callees", callees)}, nil
}

// symbolStack is the doc stack of a symbol, outermost first, nested by
// depth.
func (c *Ctx) symbolStack(sy store.Symbol) ([]output.Entry, error) {
	r, err := c.repoOf(sy)
	if err != nil {
		return nil, err
	}
	out, err := c.moduleStack(r, sy.RepoID, sy.ModuleID, false)
	if err != nil {
		return nil, err
	}
	if sy.ContainerID != 0 {
		cont, err := c.Store.SymbolByID(sy.ContainerID)
		if err != nil {
			return nil, err
		}
		e := symbolEntry(cont)
		e.Doc, e.Depth = firstParagraph(cont.DocText), len(out)
		out = append(out, e)
	}
	e := docEntry(sy)
	e.Depth = len(out)
	return append(out, e), nil
}

// moduleStack returns the workspace and repo entries and the chain of
// modules from the outermost ancestor of moduleID down to it, each with
// its first doc paragraph, or with full the whole doc of the last one.
func (c *Ctx) moduleStack(r *config.Repo, repoID, moduleID int64, full bool) ([]output.Entry, error) {
	repo := repoEntry(r)
	repo.Depth = 1
	out := []output.Entry{c.workspaceEntry(), repo}
	mods, err := c.Store.ModulesByRepo(repoID)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]store.Module, len(mods))
	for _, m := range mods {
		byID[m.ID] = m
	}
	var chain []store.Module
	for id := moduleID; id != 0 && len(chain) <= len(mods); id = byID[id].ParentID {
		m, ok := byID[id]
		if !ok {
			break
		}
		chain = append([]store.Module{m}, chain...)
	}
	for i, m := range chain {
		e := stackModuleEntry(m)
		if full && i == len(chain)-1 {
			e.Doc = strings.Join(strings.Fields(m.DocText), " ")
		}
		e.Depth = i + 2
		out = append(out, e)
	}
	return out, nil
}

// workspaceEntry is the top of a doc stack: the workspace with its repos,
// each followed by the first sentence of its README when it has one.
func (c *Ctx) workspaceEntry() output.Entry {
	parts := make([]string, len(c.Workspace.Repos))
	names := make([]string, len(c.Workspace.Repos))
	for i, r := range c.Workspace.Repos {
		names[i], parts[i] = r.Name, r.Name
		if s := firstSentence(repoEntry(r).Doc); s != "" {
			parts[i] += " (" + s + ")"
		}
	}
	return output.Entry{Key: c.Workspace.Name, Text: "workspace", Doc: "repos: " + strings.Join(parts, "; "),
		Extra: map[string]any{"kind": "workspace", "repos": names}}
}

// repoEntry is the top of a doc stack: the repo with the first paragraph
// of its README.
func repoEntry(r *config.Repo) output.Entry {
	e := output.Entry{Key: r.Name, Text: "repo"}
	src, err := os.ReadFile(filepath.Join(r.Path, "README.md"))
	if err == nil {
		e.Loc, e.Doc = r.Name+":README.md", firstParagraph(markdownText(string(src)))
	}
	return e
}

// stackModuleEntry renders a module of a doc stack with its first doc
// paragraph and its doc state when not ok.
func stackModuleEntry(m store.Module) output.Entry {
	e := output.Entry{Key: m.Identity, Loc: m.Path, RepoID: m.RepoID, Text: "module " + m.Lang + " " + m.Kind,
		Doc: firstParagraph(m.DocText), Extra: map[string]any{"kind": "module", "doc_state": m.DocState}}
	if m.DocState == "missing" || m.DocState == "stale" {
		e.Tags = []string{docTag(m.DocState)}
	}
	return e
}

// markdownText drops heading lines, badge lines and HTML comments from
// Markdown, keeping its prose.
func markdownText(src string) string {
	var b strings.Builder
	for _, l := range strings.Split(src, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "#") || strings.HasPrefix(t, "[![") || strings.HasPrefix(t, "<!--") {
			b.WriteString("\n")
			continue
		}
		b.WriteString(l + "\n")
	}
	return b.String()
}

// firstParagraph returns the first non-blank paragraph of a doc on one
// line.
func firstParagraph(doc string) string {
	for _, p := range strings.Split(strings.ReplaceAll(doc, "\r\n", "\n"), "\n\n") {
		if t := strings.Join(strings.Fields(p), " "); t != "" {
			return t
		}
	}
	return ""
}

// rankedNeighbours returns the distinct callers (inbound) or resolved
// callees of a symbol, highest fan-in first, at most explainTop, each
// with its fan-in.
func (c *Ctx) rankedNeighbours(id int64, inbound bool) ([]output.Entry, error) {
	var edges []store.Edge
	var err error
	if inbound {
		edges, err = c.Store.EdgesTo(id, "calls")
	} else {
		edges, err = c.Store.EdgesFrom(id, "calls")
	}
	if err != nil {
		return nil, err
	}
	var ids []int64
	for _, e := range edges {
		other := e.Src
		if !inbound {
			other = e.Dst // 0 for an ambiguous call, which SymbolsByID skips
		}
		ids = append(ids, other)
	}
	syms, err := c.Store.SymbolsByID(ids)
	if err != nil {
		return nil, err
	}
	return c.topByFanIn(syms, explainTop)
}

// topByFanIn renders the n symbols with the highest fan-in, tests after
// the rest (ties by stable key), with their fan-in and doc state tags.
func (c *Ctx) topByFanIn(syms map[int64]store.Symbol, n int) ([]output.Entry, error) {
	type ranked struct {
		sy    store.Symbol
		fanIn int
	}
	ids := make([]int64, 0, len(syms))
	for id := range syms {
		ids = append(ids, id)
	}
	fanIn, err := c.Store.FanIns(ids)
	if err != nil {
		return nil, err
	}
	list := make([]ranked, 0, len(syms))
	for _, sy := range syms {
		list = append(list, ranked{sy, fanIn[sy.ID]})
	}
	sort.Slice(list, func(i, j int) bool {
		if ti, tj := isTestSymbol(list[i].sy), isTestSymbol(list[j].sy); ti != tj {
			return tj
		}
		if list[i].fanIn != list[j].fanIn {
			return list[i].fanIn > list[j].fanIn
		}
		return list[i].sy.StableKey < list[j].sy.StableKey
	})
	out := make([]output.Entry, 0, min(n, len(list)))
	for _, r := range list[:min(n, len(list))] {
		e := symbolEntry(r.sy)
		e.Tags = append(append(e.Tags, fmt.Sprintf("fan-in:%d", r.fanIn)), docTags(r.sy)...)
		out = append(out, e)
	}
	return out, nil
}

// explainPath explains a file or a module named by a path or module
// identity; ok is false when target names neither.
func (c *Ctx) explainPath(target string) (*output.Response, bool, error) {
	if r, p, err := c.splitTarget(target); err == nil {
		if resp, ok, err := c.explainAt(r, p); ok || err != nil {
			return resp, ok, err
		}
	}
	mods, err := c.Store.ModulesByIdentity(strings.TrimSuffix(target, "/"))
	if err != nil || len(mods) == 0 {
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
	resp, err := c.explainModule(r, mods[0])
	return resp, true, err
}

// explainAt explains the file or the module directory at a path of a
// repo; ok is false when there is neither.
func (c *Ctx) explainAt(r *config.Repo, p string) (*output.Response, bool, error) {
	if err := c.EnsureFresh(r); err != nil {
		return nil, false, err
	}
	id, err := c.repoID(r)
	if err != nil || id == 0 {
		return nil, false, err
	}
	f, err := c.Store.FileByPath(id, p)
	if err == nil {
		resp, err := c.explainFile(r, f)
		return resp, true, err
	}
	if !errors.Is(err, store.ErrNotFound) {
		return nil, false, err
	}
	mods, err := c.modulesAt(r.Name, p)
	if err != nil || len(mods) == 0 {
		return nil, false, err
	}
	resp, err := c.explainModule(r, mods[0])
	return resp, true, err
}

// explainModule prints a module's stack with its full doc, then its
// top-level symbols with the highest fan-in, tests last.
func (c *Ctx) explainModule(r *config.Repo, m store.Module) (*output.Response, error) {
	stack, err := c.moduleStack(r, m.RepoID, m.ID, true)
	if err != nil {
		return nil, err
	}
	syms, err := c.Store.SymbolsInModule(m.ID)
	if err != nil {
		return nil, err
	}
	top := map[int64]store.Symbol{}
	for _, sy := range syms {
		if sy.ContainerID == 0 {
			top[sy.ID] = sy
		}
	}
	entries, err := c.topByFanIn(top, 2*explainTop)
	if err != nil {
		return nil, err
	}
	return c.respond([]*config.Repo{r}, output.Section{Title: "explain " + m.Identity, Entries: stack},
		c.section("main symbols", entries)), nil
}

// explainFile prints the stack of a file's module and the file's
// top-level symbols in line order.
func (c *Ctx) explainFile(r *config.Repo, f store.File) (*output.Response, error) {
	syms, err := c.Store.SymbolsInFile(f.ID)
	if err != nil {
		return nil, err
	}
	if parse.IsDoc(f.Lang) {
		return c.explainDocument(r, f, syms), nil
	}
	secs, err := c.fileExplained(r, f, syms)
	if err != nil {
		return nil, err
	}
	return c.respond([]*config.Repo{r}, secs...), nil
}

// fileExplained returns the sections that explain a code file holding
// syms: the stack of its module and its top-level symbols.
func (c *Ctx) fileExplained(r *config.Repo, f store.File, syms []store.Symbol) ([]output.Section, error) {
	moduleID := int64(0)
	var entries []output.Entry
	for _, sy := range syms {
		moduleID = max(moduleID, sy.ModuleID)
		if sy.ContainerID == 0 {
			e := symbolEntry(sy)
			e.Tags = append(e.Tags, docTags(sy)...)
			entries = append(entries, e)
		}
	}
	if moduleID == 0 {
		if mods, err := c.modulesAt(r.Name, path.Dir(f.Path)); err == nil && len(mods) > 0 {
			moduleID = mods[0].ID
		}
	}
	stack, err := c.moduleStack(r, f.RepoID, moduleID, false)
	if err != nil {
		return nil, err
	}
	return []output.Section{{Title: "explain " + r.Name + ":" + f.Path, Entries: stack}, c.section("symbols", entries)}, nil
}

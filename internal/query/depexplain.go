package query

import (
	"fmt"
	"strings"

	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/store"
)

// namesDependency reports whether a target may name a package of the
// workspace or one of its modules: a package id, name@version or name,
// or a specifier below a name. It reads the dependency uses only, so a
// target that names none never opens deps.db.
func (c *Ctx) namesDependency(target string) bool {
	uses, err := c.Store.DepUses(0)
	if err != nil {
		return false
	}
	for _, u := range uses {
		name := u.PackageName()
		if u.State == store.DepIndexed && (target == name || target == u.PackageID() ||
			target == name+"@"+u.Version || strings.HasPrefix(target, name+"/")) {
			return true
		}
	}
	return false
}

// depExplainPackage explains a package (its id, name@version or name)
// or one of its modules (an import specifier): the package with the
// first paragraph of its README, the module with its doc, then the
// symbols of it the workspace uses most, or without any use its first
// top-level symbols. ok is false when target names neither.
func (c *Ctx) depExplainPackage(target string) (*output.Response, bool, error) {
	name := strings.TrimSuffix(target, "/")
	if !c.namesDependency(name) {
		return nil, false, nil
	}
	repos := c.scope()
	if err := c.EnsureFresh(repos...); err != nil {
		return nil, false, err
	}
	v := c.deps()
	if v == nil {
		return nil, false, nil
	}
	var secs []output.Section
	for _, p := range v.pkgs {
		m, ok, err := v.moduleNamed(p, name)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			continue
		}
		stack, err := v.stack(p, m, true)
		if err != nil {
			return nil, false, err
		}
		main, err := c.depMainSymbols(v, p, m)
		if err != nil {
			return nil, false, err
		}
		secs = append(secs, output.Section{Title: "explain " + m.Identity + " [" + p.tag() + "]", Entries: stack}, main)
	}
	if len(secs) == 0 {
		return nil, false, nil
	}
	return c.respond(repos, secs...), true, nil
}

// moduleNamed returns the module of a package a target names: the root
// module for the package itself, else the module of an import specifier.
func (v *depView) moduleNamed(p *depPkg, name string) (store.Module, bool, error) {
	mods, err := v.s.ModulesByRepo(p.repoID)
	if err != nil {
		return store.Module{}, false, err
	}
	ids, err := v.s.RepoIdentities(p.repoID)
	if err != nil {
		return store.Module{}, false, err
	}
	path := ""
	switch {
	case name == p.id.String() || name == p.id.Name || name == p.id.Name+"@"+p.id.Version:
		path = "."
	default:
		for _, id := range ids {
			if id.Identity == name {
				path = id.Path
			}
		}
	}
	for _, m := range mods {
		if m.Path == path {
			return m, true, nil
		}
	}
	return store.Module{}, false, nil
}

// stack is the doc stack of a module of a package, outermost first: the
// package with the first paragraph of its README, then, for another
// module than the root, the module with the first paragraph of its doc
// (the whole doc with full).
func (v *depView) stack(p *depPkg, m store.Module, full bool) ([]output.Entry, error) {
	root, _, err := v.moduleNamed(p, p.id.String())
	if err != nil {
		return nil, err
	}
	out := []output.Entry{{Key: p.id.String(), Text: "package", Doc: readmeLead(root.DocText),
		Tags: []string{p.tag()}, Extra: map[string]any{"kind": "package", "dep": p.id.String(), "dir": p.root()}}}
	if m.ID == root.ID {
		return out, nil
	}
	e := output.Entry{Key: m.Identity, Loc: m.Path, Text: "module " + m.Lang + " " + m.Kind, Depth: 1,
		Doc: firstParagraph(m.DocText), Extra: map[string]any{"kind": "module", "dep": p.id.String()}}
	if full {
		e.Doc = strings.Join(strings.Fields(m.DocText), " ")
	}
	return append(out, e), nil
}

// depMainSymbols lists what of a package the workspace uses: for the
// root module the symbols of the whole package with the most edges from
// workspace code, for another module its own; without any use, the
// first top-level symbols of the module.
func (c *Ctx) depMainSymbols(v *depView, p *depPkg, m store.Module) (output.Section, error) {
	used, err := c.Store.DepKeysUsed(p.id.String(), nodeCap)
	if err != nil {
		return output.Section{}, err
	}
	var entries []output.Entry
	for _, u := range used {
		d, ok, err := v.byKey(u.Key)
		if err != nil {
			return output.Section{}, err
		}
		if ok && (m.Path == "." || d.ModuleID == m.ID) && len(entries) < 2*explainTop {
			e := depEntry(d)
			e.Tags = []string{fmt.Sprintf("fan-in:%d", u.N)}
			entries = append(entries, e)
		}
	}
	if len(entries) > 0 {
		return c.tagged("used by the workspace", entries), nil
	}
	entries, err = v.firstSymbols(p, m)
	return c.section("main symbols", entries), err
}

// firstSymbols lists the first top-level symbols of a module.
func (v *depView) firstSymbols(p *depPkg, m store.Module) ([]output.Entry, error) {
	syms, err := v.s.SymbolsInModule(m.ID)
	if err != nil {
		return nil, err
	}
	var entries []output.Entry
	for _, sy := range syms {
		if sy.ContainerID == 0 && len(entries) < 2*explainTop {
			e := depEntry(depSym{Symbol: sy, pkg: p})
			e.Tags = nil
			entries = append(entries, e)
		}
	}
	return entries, nil
}

// depExplain explains a dependency symbol: its package and module, its
// container, its signature and full doc, then the workspace symbols that
// call it most (deps.db holds no callees).
func (c *Ctx) depExplain(d depSym) (*output.Response, error) {
	v := c.view
	mods, err := v.s.ModulesByID([]int64{d.ModuleID})
	if err != nil {
		return nil, err
	}
	stack, err := v.stack(d.pkg, mods[d.ModuleID], false)
	if err != nil {
		return nil, err
	}
	if d.ContainerID != 0 {
		cont, err := v.s.SymbolByID(d.ContainerID)
		if err != nil {
			return nil, err
		}
		e := depEntry(depSym{Symbol: cont, pkg: d.pkg})
		e.Doc, e.Depth, e.Tags = firstParagraph(cont.DocText), len(stack), nil
		stack = append(stack, e)
	}
	e := depDocEntry(d)
	e.Depth, e.Tags = len(stack), e.Tags[1:]
	edges, err := c.Store.EdgesToDep(d.StableKey, "calls")
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(edges))
	for i, edge := range edges {
		ids[i] = edge.Src
	}
	srcs, err := c.Store.SymbolsByID(ids)
	if err != nil {
		return nil, err
	}
	callers, err := c.topByFanIn(srcs, explainTop)
	if err != nil {
		return nil, err
	}
	return c.respond(c.Workspace.Repos, output.Section{Title: "explain " + shortName(d.Symbol),
		KeyTitle: "explain " + d.StableKey, Entries: append(stack, e)},
		c.section("top callers", callers)), nil
}

// leadMax is the most bytes of a README a package entry quotes.
const leadMax = 320

// readmeLead returns the first paragraph of a package README as one
// line of prose: headings, badges, images, HTML lines and quote markers
// dropped, cut after the last word within leadMax bytes.
func readmeLead(doc string) string {
	var lines []string
	for _, l := range strings.Split(markdownText(doc), "\n") {
		l = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(l), ">"))
		if strings.HasPrefix(l, "<") || strings.HasPrefix(l, "![") {
			l = ""
		}
		lines = append(lines, l)
	}
	lead := firstParagraph(strings.Join(lines, "\n"))
	if len(lead) <= leadMax {
		return lead
	}
	cut := strings.LastIndexByte(lead[:leadMax], ' ')
	if cut <= 0 {
		cut = leadMax
	}
	return strings.ToValidUTF8(lead[:cut], "") + " ..."
}

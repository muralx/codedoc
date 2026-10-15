package query

import (
	"fmt"
	"strings"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/docs"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/parse"
	"github.com/muralx/codedoc/internal/store"
)

// howDepth is how many calls deep the call tree of a how answer goes;
// howFlows how many entry functions of a file get one.
const (
	howDepth = 2
	howFlows = 3
)

// How answers "how does this work" in one answer under one budget: what
// Explain says about a symbol or a path, then the first two levels of
// the call tree Flow prints. For a symbol they are its doc stack, its
// top callers and the tree below it (which lists its callees in call
// order, so the top callees of explain are left out). For a code file
// that holds entry functions (see entryFunctions) each of the first
// three gets its tree. Anything else flow has nothing to say about (a
// directory, a module, a document, a dependency) is answered as Explain
// answers it. The budget is shared among the sections (see Ctx.share),
// and a section that is cut names the command that prints it alone.
func How(c *Ctx, target string) (*output.Response, error) {
	if a := ParseAddr(target); a.Kind == AddrBare {
		if resp, ok, err := c.howPath(target); ok || err != nil {
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
	addr := c.resolvedAddr(target, t.sym)
	secs, err := c.symbolExplained(t.sym)
	if err != nil {
		return nil, err
	}
	secs = commanded(secs[:2], "codedoc explain "+addr+c.Scope)
	tree, err := c.howFlow(t.sym, addr)
	if err != nil {
		return nil, err
	}
	return c.share(c.respond(c.Workspace.Repos, append(secs, tree...)...)), nil
}

// howPath answers How for a target that names no symbol but a code file
// (see howFile), or what Explain explains by its path or name: a
// directory, a module, a document, a package of the dependencies. ok is
// false when target names none of them.
func (c *Ctx) howPath(target string) (*output.Response, bool, error) {
	f, r, err := c.fileAt(target)
	if err != nil {
		return nil, false, err
	}
	if r != nil && !parse.IsDoc(f.Lang) {
		resp, err := c.howFile(r, f, target)
		return resp, true, err
	}
	if resp, ok, err := c.explainPath(target); ok || err != nil {
		return resp, ok, err
	}
	if resp, ok, err := c.depExplainPackage(target); ok || err != nil {
		return resp, ok, err
	}
	return c.howNearFile(target)
}

// howNearFile answers How for a target written as a code file (bin.ts,
// src/bin.ts) that is no path from here: when exactly one indexed file
// may be meant (see pathsLike) it is answered, and the note says so;
// several are listed as the nearest candidates, with ErrNearest.
func (c *Ctx) howNearFile(target string) (*output.Response, bool, error) {
	if c.noNearest() || strings.Contains(target, ":") || parse.LanguageForPath(target) == "" {
		return nil, false, nil
	}
	if err := c.EnsureFresh(c.scope()...); err != nil {
		return nil, false, err
	}
	found, _, err := c.pathsLike(c.scope(), target, false)
	if err != nil || len(found) == 0 {
		return nil, false, err
	}
	if len(found) > 1 {
		return c.respond(c.scope(), c.section(nearestTitle, c.pathEntries(found))), true,
			fmt.Errorf("how %s: %w", target, ErrNearest)
	}
	resp, ok, err := c.howPath(found[0].addr())
	if resp != nil && err == nil {
		note := "answered for " + found[0].addr()
		if resp.Note != "" {
			note += ". " + resp.Note
		}
		resp.Note = note
	}
	return resp, ok, err
}

// commanded names command as the one that prints each of secs alone.
func commanded(secs []output.Section, command string) []output.Section {
	for i := range secs {
		secs[i].Command = command
	}
	return secs
}

// howFlow is the call tree below sy to howDepth, none when sy calls
// nothing. Its first line names sy without the signature and the doc
// the explanation above it already printed.
func (c *Ctx) howFlow(sy store.Symbol, addr string) ([]output.Section, error) {
	sec, err := c.flowSection(sy, FlowOpts{Depth: howDepth})
	if err != nil || len(sec.Entries) < 2 {
		return nil, err
	}
	sec.Entries[0].Hide = output.HideText
	sec.Command = fmt.Sprintf("codedoc flow %s --depth %d%s", addr, howDepth, c.Scope)
	return []output.Section{sec}, nil
}

// howFile explains a code file and prints the call tree of its entry
// functions.
func (c *Ctx) howFile(r *config.Repo, f store.File, target string) (*output.Response, error) {
	if err := c.EnsureFresh(c.Workspace.Repos...); err != nil {
		return nil, err
	}
	syms, err := c.Store.SymbolsInFile(f.ID)
	if err != nil {
		return nil, err
	}
	secs, err := c.fileExplained(r, f, syms)
	if err != nil {
		return nil, err
	}
	secs = commanded(secs, "codedoc explain "+shellQuote(target)+c.Scope)
	roots, err := c.entryFunctions(f, syms)
	if err != nil {
		return nil, err
	}
	c.draws = append(c.draws, f.RepoID)
	for i, sy := range roots {
		if i == howFlows {
			var rest []string
			for _, more := range roots[i:] {
				rest = append(rest, shortName(more))
			}
			c.notes = append(c.notes, fmt.Sprintf("%d more entry %s without their call tree: %s; codedoc flow <name> prints one",
				len(rest), plural(len(rest), "function", "functions"), strings.Join(rest, ", ")))
			break
		}
		tree, err := c.howFlow(sy, shellQuote(r.Name+":"+f.Path+"#"+sy.Qualified[strings.LastIndex(sy.Qualified, "/")+1:]))
		if err != nil {
			return nil, err
		}
		secs = append(secs, tree...)
	}
	return c.share(c.respond(c.Workspace.Repos, secs...)), nil
}

// entryFunctions returns the functions of a code file where its work
// starts: the top-level functions, and the methods named main, that
// call something and that no symbol of the workspace calls (the file
// runs them itself, or something outside the index does). A test file
// has none: its tests are no flow of the program.
func (c *Ctx) entryFunctions(f store.File, syms []store.Symbol) ([]store.Symbol, error) {
	if docs.IsTestPath(f.Path) {
		return nil, nil
	}
	var out []store.Symbol
	for _, sy := range syms {
		entry := sy.Kind == "function" && sy.ContainerID == 0 || sy.Kind == "method" && sy.Name == "main"
		if sy.IsTest || !entry {
			continue
		}
		in, err := c.Store.EdgesTo(sy.ID, "calls")
		if err != nil {
			return nil, err
		}
		calls, err := c.Store.EdgesFrom(sy.ID, "calls")
		if err != nil {
			return nil, err
		}
		if len(in) == 0 && len(calls) > 0 {
			out = append(out, sy)
		}
	}
	return out, nil
}

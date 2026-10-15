package query

import (
	"strings"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/parse"
	"github.com/muralx/codedoc/internal/store"
)

// FindOpts filter a find.
type FindOpts struct {
	Kind string // symbol kind, "" for any
	Lang string // language family (go, ts) or precise language, "" for any
	// Doc prints the whole doc of each symbol in place of its first
	// sentence.
	Doc bool
}

// Find lists the symbols matching a name, a qualified name, or a glob
// (*, ?, [...]; matched against the qualified name when it holds a dot
// or a slash),
// across the workspace or the --repo repo, production code before tests;
// with Ctx.Detail each is tagged with its kind, fan-in and doc state.
// With Ctx.Deps, or when no workspace symbol matches, the symbols of the
// dependencies follow, grouped by package version, with their doc
// sentence only with --detail or Doc; a member a dependency type
// inherits is found through the type (Server.notification) and says
// where it is declared. A name or words nothing matches are answered by
// Search (see findBySearch); a glob that matches nothing, and any
// pattern under Ctx.Strict, is an empty answer.
func Find(c *Ctx, pattern string, o FindOpts) (*output.Response, error) {
	repos := c.scope()
	if err := c.EnsureFresh(repos...); err != nil {
		return nil, err
	}
	syms, err := c.match(pattern, o.Kind)
	if err != nil {
		return nil, err
	}
	var entries []output.Entry
	var kept []store.Symbol
	for _, sy := range syms {
		if o.speaks(sy.Lang) {
			e := symbolEntry(sy)
			e.Tags = append(e.Tags, kindTags(sy)...)
			if o.Doc {
				e.Doc = wholeDoc(sy.DocText)
			}
			entries, kept = append(entries, e), append(kept, sy)
		}
	}
	if err := c.detailed(entries, kept); err != nil {
		return nil, err
	}
	entries = testsLast(entries)
	if c.Deps || len(entries) == 0 {
		found, err := c.findInDeps(pattern, o)
		if err != nil {
			return nil, err
		}
		entries = append(entries, found...)
	}
	if len(entries) == 0 && c.searchesFor(pattern, o) {
		return c.findBySearch(pattern, o)
	}
	return c.respond(repos, c.section("find "+pattern, entries)), nil
}

// findInDeps lists the dependency symbols a find pattern names, in
// package, path and line order.
func (c *Ctx) findInDeps(pattern string, o FindOpts) ([]output.Entry, error) {
	found, err := c.deps().match(pattern, o.Kind)
	if err != nil {
		return nil, err
	}
	sortDeps(found)
	var entries []output.Entry
	for _, d := range found {
		if !o.speaks(d.Lang) {
			continue
		}
		e := depEntry(d)
		if e.Hide = output.HideDoc; o.Doc {
			e.Doc, e.Hide = wholeDoc(d.DocText), 0
		}
		if c.Detail {
			e.Tags = append([]string{d.Kind}, e.Tags...)
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// speaks reports whether a symbol of a precise language passes the
// language filter.
func (o FindOpts) speaks(lang string) bool {
	return o.Lang == "" || lang == o.Lang || parse.Family(lang) == o.Lang
}

// searchesFor reports whether a find pattern nothing matched is handed
// to Search: a name or words (a glob is a precise question, and so is a
// heading asked for with a document kind), unless resolution is strict.
func (c *Ctx) searchesFor(pattern string, o FindOpts) bool {
	return !c.Strict && !strings.ContainsAny(pattern, "*?[") && !extract.IsDocKind(o.Kind)
}

// noSymbolNamed titles a find answered by Search.
const noSymbolNamed = "no symbol named so; search results:"

// findBySearch answers a find no symbol matched with the search for its
// pattern (efficiency spec section 4): the answer codedoc search gives,
// under the kind and language filters search knows. A pattern of one
// word says so in the title; several words without a filter ask where
// something is done, and get the answer of Where: the top locations with
// the lines that matched.
func (c *Ctx) findBySearch(pattern string, o FindOpts) (*output.Response, error) {
	if len(strings.Fields(pattern)) > 1 && o.Kind == "" && o.Lang == "" {
		return Where(c, pattern, WhereOpts{})
	}
	so := SearchOpts{Kind: o.Kind, Lang: o.Lang}
	if checkSearchOpts(so) != nil {
		so = SearchOpts{}
	}
	resp, err := Search(c, pattern, so)
	if err == nil && len(resp.Sections) > 0 && len(strings.Fields(pattern)) == 1 {
		resp.Sections[0].Title, resp.Sections[0].KeyTitle = noSymbolNamed, ""
	}
	return resp, err
}

// match finds the workspace symbols a find pattern names.
func (c *Ctx) match(pattern, kind string) ([]store.Symbol, error) {
	l, err := c.workspace()
	if err != nil {
		return nil, err
	}
	return l.match(pattern, kind)
}

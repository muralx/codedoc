package resolve

import (
	"cmp"
	"slices"
	"strings"

	"github.com/muralx/codedoc/internal/parse"
	"github.com/muralx/codedoc/internal/store"
)

// targetIndex holds the candidate symbols of one resolution run indexed
// the ways the tiers look them up, so a reference costs lookups in small
// lists rather than a scan of every symbol sharing its name.
type targetIndex struct {
	named    map[nameKey][]store.Target // name, family
	byFile   map[scopeKey][]store.Target
	byModule map[scopeKey][]store.Target
	byQual   map[scopeKey][]store.Target // qualified name, module
	families map[string]string
}

type nameKey struct{ name, fam string }

type scopeKey struct {
	name string
	id   int64
}

func newTargetIndex(targets []store.Target) *targetIndex {
	x := &targetIndex{named: map[nameKey][]store.Target{}, byFile: map[scopeKey][]store.Target{},
		byModule: map[scopeKey][]store.Target{}, byQual: map[scopeKey][]store.Target{}, families: map[string]string{}}
	for _, t := range targets {
		k := nameKey{t.Name, x.family(t.Lang)}
		x.named[k] = append(x.named[k], t)
		x.byFile[scopeKey{t.Name, t.FileID}] = append(x.byFile[scopeKey{t.Name, t.FileID}], t)
		x.byModule[scopeKey{t.Name, t.ModuleID}] = append(x.byModule[scopeKey{t.Name, t.ModuleID}], t)
		x.byQual[scopeKey{t.Qualified, t.ModuleID}] = append(x.byQual[scopeKey{t.Qualified, t.ModuleID}], t)
	}
	return x
}

// family is parse.Family, cached per language.
func (x *targetIndex) family(lang string) string {
	f, ok := x.families[lang]
	if !ok {
		f = parse.Family(lang)
		x.families[lang] = f
	}
	return f
}

// candidates are the symbols one reference may name: its name (or the
// name an import alias stands for), language family and symbol kinds.
type candidates struct {
	x         *targetIndex
	name, fam string
	kinds     []string
}

// keep returns the targets of ts of the candidates' family and kinds.
func (c candidates) keep(ts []store.Target) []store.Target {
	var out []store.Target
	for _, t := range ts {
		if slices.Contains(c.kinds, t.Kind) && c.x.family(t.Lang) == c.fam {
			out = append(out, t)
		}
	}
	return out
}

// any reports whether some symbol may be named.
func (c candidates) any() bool {
	return slices.ContainsFunc(c.x.named[nameKey{c.name, c.fam}], func(t store.Target) bool {
		return slices.Contains(c.kinds, t.Kind)
	})
}

// all returns every candidate of the workspace, by id.
func (c candidates) all() []store.Target { return c.keep(c.x.named[nameKey{c.name, c.fam}]) }

// inFile returns the candidates declared in a file.
func (c candidates) inFile(id int64) []store.Target { return c.keep(c.x.byFile[scopeKey{c.name, id}]) }

// inModules returns the candidates declared in the modules of set, by id.
func (c candidates) inModules(set map[int64]bool) []store.Target {
	named := c.x.named[nameKey{c.name, c.fam}]
	if len(set) >= len(named) {
		return c.keep(filter(named, func(t store.Target) bool { return set[t.ModuleID] }))
	}
	var out []store.Target
	for id := range set {
		out = append(out, c.keep(c.x.byModule[scopeKey{c.name, id}])...)
	}
	return byID(out)
}

// member returns the candidates with a qualified name declared in the
// modules of set, by id.
func (c candidates) member(qualified string, set map[int64]bool) []store.Target {
	var out []store.Target
	for id := range set {
		out = append(out, c.keep(c.x.byQual[scopeKey{qualified, id}])...)
	}
	return byID(out)
}

// memberOf returns the candidates in the modules of set whose qualified
// name ends with the member path .qualifier.name (or is qualifier.name).
func (c candidates) memberOf(qualifier string, set map[int64]bool) []store.Target {
	want := qualifier + "." + c.name
	return filter(c.inModules(set), func(t store.Target) bool {
		return t.Qualified == want || strings.HasSuffix(t.Qualified, "."+want) || strings.HasSuffix(t.Qualified, "/"+want)
	})
}

func byID(ts []store.Target) []store.Target {
	slices.SortFunc(ts, func(a, b store.Target) int { return cmp.Compare(a.ID, b.ID) })
	return ts
}

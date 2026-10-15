package scip

import (
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/muralx/codedoc/internal/resolve"
	"github.com/muralx/codedoc/internal/store"
)

// mapDefinitions maps each SCIP symbol defined in a trusted file to our
// symbol of that file whose span contains the definition line and whose
// name is the identifier at the occurrence (the innermost one when
// several are). A symbol takes the first SCIP symbol that maps to it.
func (m *merger) mapDefinitions() {
	taken := map[int64]bool{}
	for _, p := range m.paths {
		f := m.files[p]
		for _, o := range m.docs[p] {
			if !o.Definition || m.defs[o.Symbol].ID != 0 {
				continue
			}
			names := identifiers(m.lines[p], o)
			sym, ok := innermost(m.syms[f.ID], o.Line, func(s store.Symbol) bool { return names[s.Name] })
			if !ok || taken[sym.ID] {
				continue
			}
			taken[sym.ID] = true
			m.defs[o.Symbol] = sym
			m.mapped = append(m.mapped, store.SCIPSymbol{StableKey: sym.StableKey, SCIP: o.Symbol,
				SymbolID: sym.ID, Kind: sym.Kind, Name: sym.Name})
		}
	}
}

// pendingRef is a reference occurrence with its target and the class
// of reference that can name it (see refKind).
type pendingRef struct {
	o     Occurrence
	dst   store.SCIPSymbol
	class string
}

// references plans the precise edges of the reference occurrences of a
// trusted file whose SCIP symbol maps to a symbol of the workspace. An
// occurrence takes the reference row at its line and column; failing
// that, once every occurrence had its turn, a row at its line with its
// name whose kind fits the target; failing that, a row is added for it.
func (m *merger) references(path string) error {
	f := m.files[path]
	refs, err := m.s.CodeReferencesInFile(f.ID)
	if err != nil {
		return err
	}
	pool := newRefPool(refs)
	var rest []pendingRef
	for _, o := range m.docs[path] {
		dst, ok := m.targets[symbolKey(o.Symbol)]
		if !o.Ref || !ok {
			continue
		}
		p := pendingRef{o: o, dst: dst, class: refKind(o.Symbol, dst.Kind)}
		if row, ok := pool.atPosition(o.Line, byteCols(m.lines[path], o), p.class); ok {
			m.matched(row, dst)
			continue
		}
		rest = append(rest, p)
	}
	for _, p := range rest {
		names := identifiers(m.lines[path], p.o)
		if row, ok := pool.named(p.o.Line, names, p.class); ok {
			m.matched(row, p.dst)
			continue
		}
		m.add(f, p, names)
	}
	return nil
}

// matched plans the precise edge of a reference row to dst.
func (m *merger) matched(row store.Reference, dst store.SCIPSymbol) {
	if resolve.EdgeKind(row.Kind) != "" {
		m.edges = append(m.edges, preciseEdge(row, row.ID, dst.SymbolID))
	}
}

// add plans a reference row, with its precise edge, for an occurrence
// the extractor recorded no row for; a file-level one gets none.
func (m *merger) add(f store.File, p pendingRef, names map[string]bool) {
	enc, ok := innermost(m.syms[f.ID], p.o.Line, func(store.Symbol) bool { return true })
	if !ok {
		return
	}
	name := addedName(names, p.dst.Name)
	m.added = append(m.added, addedRef{dst: p.dst.SymbolID, ref: store.Reference{FileID: f.ID, SymbolID: enc.ID,
		Kind: p.class, Name: name, Line: p.o.Line, Col: nameCol(m.lines[f.Path], p.o, name), Arity: -1}})
}

// nameCol is the byte column at which name starts at an occurrence, or
// the occurrence's own column when it is not found there.
func nameCol(lines []string, o Occurrence, name string) int {
	for _, c := range byteCols(lines, o) {
		if l := lines[o.Line-1]; c+len(name) <= len(l) && l[c:c+len(name)] == name {
			return c
		}
	}
	return o.Col
}

// addedName is the name of an added reference row: the identifier at the
// occurrence (an alias, say), or the target's name when it is one of the
// readings or the reading is unclear.
func addedName(names map[string]bool, target string) string {
	if len(names) == 1 && !names[target] {
		return sortedNames(names)[0]
	}
	return target
}

// implementations plans a precise implements edge for each
// implementation relationship of a type defined in a trusted file to a
// mapped symbol, unless an extends edge already links the pair (a Python
// base class is both).
func (m *merger) implementations() error {
	syms := make([]string, 0, len(m.defs))
	for sym := range m.defs {
		syms = append(syms, sym)
	}
	sort.Strings(syms)
	for _, sym := range syms {
		if !strings.HasSuffix(sym, "#") {
			continue // methods implement interface methods; edges link types
		}
		src := m.defs[sym]
		extends, err := m.s.EdgesFrom(src.ID, "extends")
		if err != nil {
			return err
		}
		for _, impl := range m.ix.Symbols[sym].Implements {
			dst, ok := m.targets[symbolKey(impl)]
			if ok && dst.SymbolID != src.ID && !linked(extends, dst.SymbolID) {
				m.impls = append(m.impls, implEdge{src: src.ID, dst: dst.SymbolID, file: src.FileID, line: src.StartLine})
			}
		}
	}
	return nil
}

// linked reports whether one of edges reaches dst.
func linked(edges []store.Edge, dst int64) bool {
	for _, e := range edges {
		if e.Dst == dst {
			return true
		}
	}
	return false
}

// callable are the symbol kinds a reference calls.
var callable = map[string]bool{"function": true, "method": true, "constructor": true}

// refKind is the reference kind of an occurrence the extractor did not
// record: a call of a function or method (SCIP method descriptors end in
// "()."), a type use ("#"), else a field or term use.
func refKind(sym, targetKind string) string {
	switch {
	case callable[targetKind] || strings.HasSuffix(sym, ")."):
		return "call"
	case strings.HasSuffix(sym, "#"):
		return "type"
	}
	return "field"
}

// innermost returns the symbol with the smallest span containing line
// that keep accepts; on a tie, the one starting later.
func innermost(syms []store.Symbol, line int, keep func(store.Symbol) bool) (store.Symbol, bool) {
	var best store.Symbol
	found := false
	for _, s := range syms {
		if s.StartLine > line || s.EndLine < line || !keep(s) {
			continue
		}
		if !found || s.EndLine-s.StartLine < best.EndLine-best.StartLine ||
			s.EndLine-s.StartLine == best.EndLine-best.StartLine && s.StartLine > best.StartLine {
			best, found = s, true
		}
	}
	return best, found
}

// identifiers returns the text an occurrence covers on its line, read
// with the column offsets as bytes, as code points and as UTF-16 units
// (indexers differ); a multi-line or out of range occurrence covers
// nothing.
func identifiers(lines []string, o Occurrence) map[string]bool {
	out := map[string]bool{}
	if o.Line < 1 || o.Line > len(lines) || o.EndLine != o.Line || o.Col < 0 || o.EndCol <= o.Col {
		return out
	}
	l := lines[o.Line-1]
	if o.EndCol <= len(l) {
		out[l[o.Col:o.EndCol]] = true
	}
	if r := []rune(l); o.EndCol <= len(r) {
		out[string(r[o.Col:o.EndCol])] = true
	}
	if u := utf16.Encode([]rune(l)); o.EndCol <= len(u) {
		out[string(utf16.Decode(u[o.Col:o.EndCol]))] = true
	}
	return out
}

func sortedNames(names map[string]bool) []string {
	out := make([]string, 0, len(names))
	for n := range names {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// symbolKey is a SCIP symbol without its package version ("scheme
// manager name version descriptors"), so a reference from another repo's
// index, which may name another version of the package, matches the
// definition. Symbols with escaped spaces in those fields are kept whole.
func symbolKey(sym string) string {
	parts := strings.SplitN(sym, " ", 5)
	if len(parts) < 5 {
		return sym
	}
	for _, p := range parts[:4] {
		if p == "" {
			return sym
		}
	}
	return parts[0] + " " + parts[1] + " " + parts[2] + " " + parts[4]
}

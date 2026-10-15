package scip

import (
	"unicode/utf16"

	"github.com/muralx/codedoc/internal/store"
)

// refPool holds a file's reference rows for matching occurrences; each
// row matches one occurrence.
type refPool struct {
	rows  []store.Reference
	taken []bool
	// byPos indexes rows by line and column, byLine by line.
	byPos  map[[2]int][]int
	byLine map[int][]int
}

func newRefPool(refs []store.Reference) *refPool {
	p := &refPool{rows: refs, taken: make([]bool, len(refs)), byPos: map[[2]int][]int{}, byLine: map[int][]int{}}
	for i, r := range refs {
		if r.Col >= 0 {
			p.byPos[[2]int{r.Line, r.Col}] = append(p.byPos[[2]int{r.Line, r.Col}], i)
		}
		p.byLine[r.Line] = append(p.byLine[r.Line], i)
	}
	return p
}

// atPosition takes the row at line whose column is one of cols, one
// whose kind fits the target class first (see fits).
func (p *refPool) atPosition(line int, cols []int, class string) (store.Reference, bool) {
	var at []int
	for _, c := range cols {
		at = append(at, p.byPos[[2]int{line, c}]...)
	}
	return p.pick(at, func(r store.Reference) bool { return fits(r.Kind, class) }, true)
}

// named takes the first row at line named one of names whose kind fits
// the target class.
func (p *refPool) named(line int, names map[string]bool, class string) (store.Reference, bool) {
	return p.pick(p.byLine[line], func(r store.Reference) bool { return names[r.Name] && fits(r.Kind, class) }, false)
}

// pick takes the first free row of idx that keep accepts, or with any
// orFirst, the first free row when none does.
func (p *refPool) pick(idx []int, keep func(store.Reference) bool, orFirst bool) (store.Reference, bool) {
	first := -1
	for _, i := range idx {
		if p.taken[i] {
			continue
		}
		if keep(p.rows[i]) {
			p.taken[i] = true
			return p.rows[i], true
		}
		if first < 0 {
			first = i
		}
	}
	if orFirst && first >= 0 {
		p.taken[first] = true
		return p.rows[first], true
	}
	return store.Reference{}, false
}

// fits reports whether a reference kind can name a target of a class
// (see refKind): calls and instantiations name callables, type uses,
// instantiations and extends or implements clauses name types, field
// uses name everything else.
func fits(kind, class string) bool {
	switch class {
	case "call":
		return kind == "call" || kind == "instantiate"
	case "type":
		return kind == "type" || kind == "instantiate" || kind == "extend" || kind == "implement"
	}
	return kind == "field"
}

// byteCols returns the byte columns an occurrence may start at on its
// line, reading its column as bytes, as code points and as UTF-16 units
// (indexers differ; see identifiers).
func byteCols(lines []string, o Occurrence) []int {
	if o.Line < 1 || o.Line > len(lines) || o.Col < 0 {
		return nil
	}
	l := lines[o.Line-1]
	out := []int{o.Col}
	add := func(c int) {
		for _, x := range out {
			if x == c {
				return
			}
		}
		out = append(out, c)
	}
	units, runes := 0, 0
	for b, r := range l {
		if runes == o.Col {
			add(b)
		}
		if units == o.Col {
			add(b)
		}
		runes++
		units += utf16.RuneLen(r)
	}
	return out
}

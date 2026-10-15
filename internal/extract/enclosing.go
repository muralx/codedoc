package extract

import (
	"sort"

	"github.com/muralx/codedoc/internal/parse"
)

// Encloser finds the innermost symbol around a byte offset in O(log n):
// symbols are sorted by start once, each knows its nearest container, and
// a lookup binary-searches the last symbol starting at or before the
// offset, then walks up containers until one covers it.
type Encloser struct {
	syms   []Symbol
	parent []int
}

// NewEncloser indexes syms for lookups; syms is not modified.
func NewEncloser(syms []Symbol) *Encloser {
	idx := make([]int, len(syms))
	for i := range idx {
		idx[i] = i
	}
	// Equal spans keep the earlier symbol outermost-last, so it wins lookups.
	sort.SliceStable(idx, func(a, b int) bool {
		sa, sb := syms[idx[a]].Span, syms[idx[b]].Span
		if sa.StartByte != sb.StartByte {
			return sa.StartByte < sb.StartByte
		}
		if sa.EndByte != sb.EndByte {
			return sa.EndByte > sb.EndByte
		}
		return idx[a] > idx[b]
	})
	e := &Encloser{syms: make([]Symbol, len(syms)), parent: make([]int, len(syms))}
	var stack []int
	for i, j := range idx {
		e.syms[i] = syms[j]
		start := syms[j].Span.StartByte
		for len(stack) > 0 && e.syms[stack[len(stack)-1]].Span.EndByte <= start {
			stack = stack[:len(stack)-1]
		}
		e.parent[i] = -1
		if len(stack) > 0 {
			e.parent[i] = stack[len(stack)-1]
		}
		stack = append(stack, i)
	}
	return e
}

// At returns the Qualified of the symbol with the smallest span that
// contains offset, or "" when no symbol does.
func (e *Encloser) At(offset uint) string {
	i := sort.Search(len(e.syms), func(i int) bool { return e.syms[i].Span.StartByte > offset }) - 1
	for i >= 0 && e.syms[i].Span.EndByte <= offset {
		i = e.parent[i]
	}
	if i < 0 {
		return ""
	}
	return e.syms[i].Qualified
}

// Line returns the 1-based line of n's start.
func Line(n parse.Node) int { return int(n.StartRow()) + 1 }

// Col returns the 0-based byte column where n starts.
func Col(n parse.Node) int { return int(n.StartColumn()) }

// IsField reports whether n is the child of parent stored under field.
func IsField(parent parse.Node, field string, n parse.Node) bool {
	if parent == nil {
		return false
	}
	c := parent.ChildByField(field)
	return c != nil && c.StartByte() == n.StartByte() && c.EndByte() == n.EndByte() && c.Kind() == n.Kind()
}

// ArgCount returns the number of arguments in an argument list node,
// ignoring comments.
func ArgCount(args parse.Node) int {
	if args == nil {
		return 0
	}
	n := 0
	for i := uint(0); i < args.NamedChildCount(); i++ {
		if args.NamedChild(i).Kind() != "comment" {
			n++
		}
	}
	return n
}

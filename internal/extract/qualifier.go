package extract

import (
	"strings"

	"github.com/muralx/codedoc/internal/parse"
)

// MaxQualifier caps the stored qualifier text in bytes.
const MaxQualifier = 120

// placeholder stands for a receiver segment that is not a plain name.
const placeholder = "()"

// ChainGrammar names the node kinds a language uses for qualifier chains.
type ChainGrammar struct {
	Names     map[string]bool      // leaf kinds kept verbatim: identifiers, this, super
	Selectors map[string][2]string // selector kind -> {object field, name field}
	Calls     map[string]string    // call kind -> callee field
	// Invocations maps a call kind that holds its receiver and method name
	// in two fields (Java's method_invocation) to {object field, name field}.
	Invocations map[string][2]string
}

// Qualifier renders the receiver n as a trailing identifier/selector chain
// such as "svc.s" or "a.b.c". A call keeps its callee chain followed by
// "()" (x.Do(...) becomes "x.Do()"), as does an invocation; any other
// expression becomes "()".
// The result is capped at MaxQualifier bytes, cut at a segment boundary
// so the trailing segments survive.
func (g ChainGrammar) Qualifier(n parse.Node, src []byte) string {
	return capChain(g.chain(n, src))
}

func (g ChainGrammar) chain(n parse.Node, src []byte) string {
	if n == nil {
		return placeholder
	}
	k := n.Kind()
	if g.Names[k] {
		return n.Text(src)
	}
	if f, ok := g.Selectors[k]; ok {
		obj, name := n.ChildByField(f[0]), n.ChildByField(f[1])
		if name != nil {
			return g.chain(obj, src) + "." + name.Text(src)
		}
	}
	if f, ok := g.Invocations[k]; ok {
		if name := n.ChildByField(f[1]); name != nil {
			if obj := n.ChildByField(f[0]); obj != nil {
				return g.chain(obj, src) + "." + name.Text(src) + placeholder
			}
			return name.Text(src) + placeholder
		}
	}
	if f, ok := g.Calls[k]; ok {
		if fn := g.chain(n.ChildByField(f), src); fn != placeholder {
			return fn + placeholder
		}
	}
	return placeholder
}

// capChain keeps the longest trailing run of whole segments that fits in
// MaxQualifier bytes.
func capChain(s string) string {
	if len(s) <= MaxQualifier {
		return s
	}
	tail := s[len(s)-MaxQualifier:]
	if i := strings.IndexByte(tail, '.'); i >= 0 && i+1 < len(tail) {
		return tail[i+1:]
	}
	return tail
}

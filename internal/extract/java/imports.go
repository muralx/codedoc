package java

import (
	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/parse"
)

// importDecl records import a.b.C as Raw a.b.C with alias C, and
// import a.b.* as Raw a.b.* without alias. Static imports look the same.
func (r *refWalker) importDecl(n parse.Node) {
	var name parse.Node
	wildcard := false
	for i := uint(0); i < n.NamedChildCount(); i++ {
		switch c := n.NamedChild(i); c.Kind() {
		case "scoped_identifier", "identifier":
			name = c
		case "asterisk":
			wildcard = true
		}
	}
	if name == nil {
		return
	}
	imp := extract.Import{Raw: compact(name.Text(r.src)), Line: extract.Line(n)}
	if wildcard {
		imp.Raw += ".*"
	} else {
		imp.Alias = r.lastSegment(name)
	}
	r.res.Imports = append(r.res.Imports, imp)
}

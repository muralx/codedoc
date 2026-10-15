package python

import (
	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/parse"
)

// importStatement records one import per module of import a.b as c, d.
func (r *refWalker) importStatement(n parse.Node) {
	for i := uint(0); i < n.NamedChildCount(); i++ {
		switch c := n.NamedChild(i); c.Kind() {
		case "dotted_name":
			r.res.Imports = append(r.res.Imports, extract.Import{Raw: c.Text(r.src), Line: extract.Line(n)})
		case "aliased_import":
			raw, alias := r.aliased(c)
			r.res.Imports = append(r.res.Imports, extract.Import{Raw: raw, Alias: alias, Line: extract.Line(n)})
		}
	}
}

// importFrom records from a.b import x, y as z as one import of a.b with
// Names x and y=z; relative modules keep their dots (..pkg), and a
// wildcard import (from m import *) has the single name "*".
func (r *refWalker) importFrom(n parse.Node) {
	module := n.ChildByField("module_name")
	if module == nil {
		return
	}
	imp := extract.Import{Raw: module.Text(r.src), Line: extract.Line(n)}
	for i := uint(0); i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		if c.StartByte() == module.StartByte() {
			continue
		}
		switch c.Kind() {
		case "dotted_name":
			imp.Names = append(imp.Names, c.Text(r.src))
		case "aliased_import":
			raw, alias := r.aliased(c)
			imp.Names = append(imp.Names, raw+"="+alias)
		case "wildcard_import":
			imp.Names = append(imp.Names, "*")
		}
	}
	r.res.Imports = append(r.res.Imports, imp)
}

// aliased returns the name and alias of an aliased_import node.
func (r *refWalker) aliased(n parse.Node) (name, alias string) {
	if c := n.ChildByField("name"); c != nil {
		name = c.Text(r.src)
	}
	if c := n.ChildByField("alias"); c != nil {
		alias = c.Text(r.src)
	}
	return name, alias
}

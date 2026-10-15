package java

import (
	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/parse"
)

// recordComponents adds, for each component of record n, a private field
// and a public accessor method of the same name (signature x()), unless
// the body declares that accessor explicitly. The field and the accessor
// both span the component; the field, added first, encloses references.
func (w *walker) recordComponents(n parse.Node, container string) {
	params := n.ChildByField("parameters")
	if params == nil {
		return
	}
	explicit := w.explicitAccessors(n.ChildByField("body"))
	for _, p := range extract.NamedChildren(params, "formal_parameter") {
		name := w.name(p)
		f := w.symbol(extract.KindField, name, container, p, false)
		f.Visibility = extract.Private
		w.add(f)
		if explicit[name] {
			continue
		}
		m := w.symbol(extract.KindMethod, name, container, p, false)
		m.Visibility, m.Doc, m.SigText = extract.Public, nil, name+"()"
		m.SigHash = w.sigHash(nil)
		w.add(m)
	}
}

// explicitAccessors returns the names of the parameterless methods a
// record body declares.
func (w *walker) explicitAccessors(body parse.Node) map[string]bool {
	names := map[string]bool{}
	if body == nil {
		return names
	}
	for _, m := range extract.NamedChildren(body, "method_declaration") {
		if params := m.ChildByField("parameters"); params == nil || params.NamedChildCount() == 0 {
			names[w.name(m)] = true
		}
	}
	return names
}

// compactConstructor adds the compact canonical constructor of a record,
// whose parameters are the record's components.
func (w *walker) compactConstructor(n parse.Node, record parse.Node, container string) {
	w.method(n, "<init>", container, false)
	w.res.Symbols[len(w.res.Symbols)-1].SigHash = w.sigHash(record.ChildByField("parameters"))
}

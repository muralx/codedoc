package python

import (
	"unicode"
	"unicode/utf8"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/parse"
)

// chains describes Python receiver expressions for qualifier text.
var chains = extract.ChainGrammar{
	Names:     map[string]bool{"identifier": true},
	Selectors: map[string][2]string{"attribute": {"object", "attribute"}},
	Calls:     map[string]string{"call": "function"},
}

// refWalker collects the references and imports of one Python file. It
// runs after symbol extraction so every reference gets its enclosing
// symbol.
type refWalker struct {
	src []byte
	enc *extract.Encloser
	res *extract.FileResult
}

// refs walks the whole tree below root in source order. The self.x fields
// of __init__ do not enclose references: the method does.
func (w *walker) refs(root parse.Node) {
	var encl []extract.Symbol
	for i, s := range w.res.Symbols {
		if !w.instance[i] {
			encl = append(encl, s)
		}
	}
	r := &refWalker{src: w.src, enc: extract.NewEncloser(encl), res: w.res}
	r.children(root)
}

func (r *refWalker) children(n parse.Node) {
	for i := uint(0); i < n.NamedChildCount(); i++ {
		r.visit(n.NamedChild(i), n)
	}
}

// visit records the reference n stands for, if any, and descends into the
// children that can hold further references.
func (r *refWalker) visit(n, parent parse.Node) {
	if r.whole(n, parent) {
		return
	}
	switch n.Kind() {
	case "call":
		r.call(n)
	case "subscript":
		r.configSubscript(n)
	}
	r.children(n)
}

// whole handles the nodes whose references are recorded without the
// generic descent, and reports whether n was one.
func (r *refWalker) whole(n, parent parse.Node) bool {
	switch n.Kind() {
	case "import_statement":
		r.importStatement(n)
	case "import_from_statement":
		r.importFrom(n)
	case "future_import_statement":
	case "decorator":
		r.decorator(n)
	case "attribute":
		if parent.Kind() != "call" || !extract.IsField(parent, "function", n) {
			r.add(extract.RefField, n.ChildByField("attribute"), n.ChildByField("object"), -1)
		}
		r.visitField(n, "object")
	case "type":
		r.types(n)
	case "argument_list":
		if parent.Kind() != "class_definition" || !extract.IsField(parent, "superclasses", n) {
			return false
		}
		r.superclasses(n)
	default:
		return false
	}
	return true
}

// visitField visits the child of n stored under field, if any.
func (r *refWalker) visitField(n parse.Node, field string) {
	if c := n.ChildByField(field); c != nil {
		r.visit(c, n)
	}
}

// decorator records @name and @a.name as a call with one argument, the
// decorated definition; @a.name(...) is an ordinary call.
func (r *refWalker) decorator(n parse.Node) {
	if n.NamedChildCount() == 0 {
		return
	}
	switch e := n.NamedChild(0); e.Kind() {
	case "identifier":
		r.add(extract.RefCall, e, nil, 1)
	case "attribute":
		r.add(extract.RefCall, e.ChildByField("attribute"), e.ChildByField("object"), 1)
		r.visitField(e, "object")
	default:
		r.visit(e, n)
	}
}

// call records a call to a name or an attribute, and an instantiation as
// well when the name is capitalized (Python has no new). Builtins are
// skipped. String literal arguments may be config keys.
func (r *refWalker) call(n parse.Node) {
	fn, args := n.ChildByField("function"), n.ChildByField("arguments")
	arity := extract.ArgCount(args)
	if args != nil && args.Kind() == "generator_expression" {
		arity = 1
	}
	var name, qualifier parse.Node
	switch {
	case fn == nil:
		return
	case fn.Kind() == "identifier":
		name = fn
	case fn.Kind() == "attribute":
		name, qualifier = fn.ChildByField("attribute"), fn.ChildByField("object")
	default:
		return
	}
	if r.add(extract.RefCall, name, qualifier, arity) && startsUpper(name.Text(r.src)) {
		r.add(extract.RefInstantiate, name, qualifier, arity)
	}
	r.configArgs(name, qualifier, args)
}

// superclasses records each class a class statement names as RefExtend;
// keyword arguments (metaclass=...) only contribute the references inside
// them.
func (r *refWalker) superclasses(list parse.Node) {
	for i := uint(0); i < list.NamedChildCount(); i++ {
		c := list.NamedChild(i)
		base := c
		if c.Kind() == "subscript" { // Generic[T]
			base = c.ChildByField("value")
		}
		switch {
		case base.Kind() == "identifier":
			r.add(extract.RefExtend, base, nil, -1)
		case base.Kind() == "attribute":
			r.add(extract.RefExtend, base.ChildByField("attribute"), base.ChildByField("object"), -1)
		default:
			r.visit(c, list)
		}
	}
}

// types records the names a type annotation uses; string annotations
// (forward references) are skipped.
func (r *refWalker) types(n parse.Node) {
	switch n.Kind() {
	case "identifier":
		r.add(extract.RefType, n, nil, -1)
	case "attribute":
		r.add(extract.RefType, n.ChildByField("attribute"), n.ChildByField("object"), -1)
	case "string":
	default:
		for i := uint(0); i < n.NamedChildCount(); i++ {
			r.types(n.NamedChild(i))
		}
	}
}

// add appends a reference named by name, qualified by qualifier's text,
// and reports whether it did: unqualified builtins are skipped.
func (r *refWalker) add(kind extract.RefKind, name, qualifier parse.Node, arity int) bool {
	if name == nil || (qualifier == nil && builtins[name.Text(r.src)]) {
		return false
	}
	ref := extract.Reference{
		Kind: kind, Name: name.Text(r.src), Enclosing: r.enc.At(name.StartByte()),
		Line: extract.Line(name), Col: extract.Col(name), Arity: arity,
	}
	if qualifier != nil {
		ref.Qualifier = chains.Qualifier(qualifier, r.src)
	}
	r.res.References = append(r.res.References, ref)
	return true
}

func startsUpper(s string) bool {
	c, _ := utf8.DecodeRuneInString(s)
	return unicode.IsUpper(c)
}

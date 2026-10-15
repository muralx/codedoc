package ts

import (
	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/parse"
)

// chains describes TS/JS receiver expressions for qualifier text.
var chains = extract.ChainGrammar{
	Names: map[string]bool{
		"identifier": true, "this": true, "super": true, "property_identifier": true, "type_identifier": true,
	},
	Selectors: map[string][2]string{
		"member_expression": {"object", "property"}, "nested_type_identifier": {"module", "name"},
		"nested_identifier": {"object", "property"},
	},
	Calls: map[string]string{"call_expression": "function"},
}

// refWalker collects the references and imports of one file. It runs after
// symbol extraction so every reference gets its enclosing symbol.
type refWalker struct {
	src []byte
	enc *extract.Encloser
	res *extract.FileResult
	// locals holds the types of the variables in scope and of the class
	// properties declared so far.
	locals *extract.Locals
}

// refs walks the whole tree below root in source order.
func (w *walker) refs(root parse.Node) {
	r := &refWalker{src: w.src, enc: extract.NewEncloser(w.res.Symbols), res: w.res, locals: extract.NewLocals()}
	r.declareTop(root)
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
	switch n.Kind() {
	case "import_statement":
		r.importStatement(n)
		return
	case "export_statement":
		r.reexport(n)
	case "call_expression":
		r.call(n)
	case "new_expression":
		r.named(extract.RefInstantiate, n.ChildByField("constructor"), extract.ArgCount(n.ChildByField("arguments")))
		r.configArgs(n.ChildByField("arguments"))
	case "member_expression", "subscript_expression":
		r.envAccess(n)
	case "extends_clause", "extends_type_clause":
		r.heritage(extract.RefExtend, n)
		return
	case "implements_clause":
		r.heritage(extract.RefImplement, n)
		return
	case "nested_type_identifier":
		r.add(extract.RefType, n.ChildByField("name"), n.ChildByField("module"), -1)
		return
	case "type_identifier":
		if !declName(n, parent) {
			r.add(extract.RefType, n, nil, -1)
		}
		return
	}
	r.descend(n)
}

// descend visits the children of n inside the scope n opens, if any
// (see scopes), and records the variables n declares once they are
// visited (see declarations).
func (r *refWalker) descend(n parse.Node) {
	kind := n.Kind()
	if scopes[kind] {
		r.locals.Push()
		defer r.locals.Pop()
		r.enter(n)
	}
	r.children(n)
	if declarations[kind] {
		r.declare(n)
	}
}

// call records a call to a name or a member; other callees only contribute
// the references inside them.
func (r *refWalker) call(n parse.Node) {
	if r.requireOrImport(n) {
		return
	}
	args, arity := n.ChildByField("arguments"), 1 // a tagged template passes one template
	if args != nil && args.Kind() == "arguments" {
		arity = extract.ArgCount(args)
		r.configArgs(args)
	}
	r.named(extract.RefCall, n.ChildByField("function"), arity)
}

// named records a reference to an identifier, or to the property of a
// member expression qualified by its object.
func (r *refWalker) named(kind extract.RefKind, n parse.Node, arity int) {
	if n == nil {
		return
	}
	switch n.Kind() {
	case "identifier", "type_identifier":
		r.add(kind, n, nil, arity)
	case "member_expression":
		r.add(kind, n.ChildByField("property"), n.ChildByField("object"), arity)
	case "nested_type_identifier":
		r.add(kind, n.ChildByField("name"), n.ChildByField("module"), arity)
	}
}

// heritage records every type an extends or implements clause names, and
// the type references inside their type arguments.
func (r *refWalker) heritage(kind extract.RefKind, clause parse.Node) {
	for i := uint(0); i < clause.NamedChildCount(); i++ {
		c := clause.NamedChild(i)
		switch c.Kind() {
		case "identifier", "type_identifier", "member_expression", "nested_type_identifier":
			r.named(kind, c, -1)
		case "generic_type":
			r.named(kind, c.ChildByField("name"), -1)
			r.children(c.ChildByField("type_arguments"))
		default:
			r.visit(c, clause)
		}
	}
}

// declName reports whether n is the name a declaration introduces (class,
// interface, type alias, type parameter) rather than a use.
func declName(n, parent parse.Node) bool {
	k := parent.Kind()
	return k != "generic_type" && k != "nested_type_identifier" && extract.IsField(parent, "name", n)
}

// add appends a reference named by name, qualified by qualifier's text.
func (r *refWalker) add(kind extract.RefKind, name, qualifier parse.Node, arity int) {
	if name == nil || (qualifier == nil && builtins[name.Text(r.src)]) {
		return
	}
	ref := extract.Reference{
		Kind: kind, Name: name.Text(r.src), Enclosing: r.enc.At(name.StartByte()),
		Line: extract.Line(name), Col: extract.Col(name), Arity: arity,
	}
	if qualifier != nil {
		ref.Qualifier = chains.Qualifier(qualifier, r.src)
		ref.RecvType = r.locals.TypeOf(ref.Enclosing, ref.Qualifier)
	}
	r.res.References = append(r.res.References, ref)
}

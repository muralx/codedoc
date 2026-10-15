package golang

import (
	"strings"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/parse"
)

// predeclared holds Go's predeclared types and builtin functions; uses of
// them are not references to anything in the index.
var predeclared = map[string]bool{
	"bool": true, "byte": true, "complex64": true, "complex128": true, "error": true,
	"float32": true, "float64": true, "int": true, "int8": true, "int16": true, "int32": true,
	"int64": true, "rune": true, "string": true, "uint": true, "uint8": true, "uint16": true,
	"uint32": true, "uint64": true, "uintptr": true, "any": true, "comparable": true,
	"append": true, "cap": true, "clear": true, "close": true, "complex": true, "copy": true,
	"delete": true, "imag": true, "len": true, "make": true, "max": true, "min": true,
	"new": true, "panic": true, "print": true, "println": true, "real": true, "recover": true,
}

// chains describes Go receiver expressions for qualifier text.
var chains = extract.ChainGrammar{
	Names: map[string]bool{
		"identifier": true, "package_identifier": true, "field_identifier": true, "type_identifier": true,
	},
	Selectors: map[string][2]string{"selector_expression": {"operand", "field"}},
	Calls:     map[string]string{"call_expression": "function"},
}

// refWalker collects the references and imports of one Go file. It runs
// after symbol extraction so every reference gets its enclosing symbol.
type refWalker struct {
	src []byte
	enc *extract.Encloser
	res *extract.FileResult
	// locals holds the types of the variables in scope.
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
	case "import_spec":
		r.importSpec(n)
		return
	case "call_expression":
		r.call(n)
	case "selector_expression":
		if parent.Kind() != "call_expression" || !extract.IsField(parent, "function", n) {
			r.add(extract.RefField, n.ChildByField("field"), n.ChildByField("operand"), -1)
		}
		if op := n.ChildByField("operand"); op != nil {
			r.visit(op, n)
		}
		return
	case "qualified_type":
		r.add(r.typeKind(n, parent), n.ChildByField("name"), n.ChildByField("package"), -1)
		return
	case "type_identifier":
		if !r.declName(n, parent) && !predeclared[n.Text(r.src)] {
			r.add(r.typeKind(n, parent), n, nil, -1)
		}
		return
	}
	r.descend(n)
}

// descend visits the children of n inside the scope n opens, if any
// (see scopes), and records the variables n declares (see
// declarations), before or after its children as their scope asks.
func (r *refWalker) descend(n parse.Node) {
	kind := n.Kind()
	if scopes[kind] {
		r.locals.Push()
		defer r.locals.Pop()
	}
	first, declares := declarations[kind]
	if declares && first {
		r.declare(n)
	}
	r.children(n)
	if declares && !first {
		r.declare(n)
	}
}

// call records a call whose function is a name or a selector. Other
// callees (function literals, index expressions) only contribute the
// references inside them.
func (r *refWalker) call(n parse.Node) {
	fn, arity := n.ChildByField("function"), extract.ArgCount(n.ChildByField("arguments"))
	if fn == nil {
		return
	}
	switch fn.Kind() {
	case "identifier":
		if !predeclared[fn.Text(r.src)] {
			r.add(extract.RefCall, fn, nil, arity)
		}
		r.configArgs(n, fn, nil)
	case "selector_expression":
		r.add(extract.RefCall, fn.ChildByField("field"), fn.ChildByField("operand"), arity)
		r.configArgs(n, fn.ChildByField("field"), fn.ChildByField("operand"))
	default:
		r.configArgs(n, nil, nil)
	}
}

// typeKind is RefInstantiate for the type of a composite literal, directly
// or through type arguments (List[int]{}), for the element type of a
// slice, array or map literal whose elements elide it ([]Foo{{}}), and
// RefType elsewhere.
func (r *refWalker) typeKind(n, parent parse.Node) extract.RefKind {
	if parent.Kind() == "generic_type" && extract.IsField(parent, "type", n) {
		n, parent = parent, parent.Parent()
	}
	if parent != nil && parent.Kind() == "composite_literal" && extract.IsField(parent, "type", n) {
		return extract.RefInstantiate
	}
	if parent != nil && parent.Kind() == "pointer_type" {
		n, parent = parent, parent.Parent()
	}
	if elidedElement(n, parent) {
		return extract.RefInstantiate
	}
	return extract.RefType
}

// elidedElement reports whether n is the element (or map key or value)
// type of a composite literal type and some element of that literal is a
// bare {...} that instantiates n implicitly.
func elidedElement(n, parent parse.Node) bool {
	if parent == nil {
		return false
	}
	slot := -1 // position inside a keyed_element: 0 key, 1 value
	switch parent.Kind() {
	case "slice_type", "array_type", "implicit_length_array_type":
		if !extract.IsField(parent, "element", n) {
			return false
		}
		slot = 1
	case "map_type":
		if extract.IsField(parent, "key", n) {
			slot = 0
		} else if extract.IsField(parent, "value", n) {
			slot = 1
		}
	}
	lit := parent.Parent()
	if slot < 0 || lit == nil || lit.Kind() != "composite_literal" || !extract.IsField(lit, "type", parent) {
		return false
	}
	return hasElidedElement(lit.ChildByField("body"), slot, parent.Kind() != "map_type")
}

// hasElidedElement reports whether a literal body holds a bare {...} in
// the given keyed slot, or as a plain element when plain is true.
func hasElidedElement(body parse.Node, slot int, plain bool) bool {
	if body == nil {
		return false
	}
	for i := uint(0); i < body.NamedChildCount(); i++ {
		e := body.NamedChild(i)
		if e.Kind() == "keyed_element" && uint(slot) < e.NamedChildCount() {
			e = e.NamedChild(uint(slot))
		} else if !plain {
			continue
		}
		if e.Kind() == "literal_element" && e.NamedChildCount() > 0 && e.NamedChild(0).Kind() == "literal_value" {
			return true
		}
	}
	return false
}

// declName reports whether n is the name a type declaration introduces.
func (r *refWalker) declName(n, parent parse.Node) bool {
	k := parent.Kind()
	return (k == "type_spec" || k == "type_alias") && extract.IsField(parent, "name", n)
}

// add appends a reference named by name, qualified by qualifier's text.
func (r *refWalker) add(kind extract.RefKind, name, qualifier parse.Node, arity int) {
	if name == nil {
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

func (r *refWalker) importSpec(n parse.Node) {
	path := n.ChildByField("path")
	if path == nil {
		return
	}
	imp := extract.Import{Raw: strings.Trim(path.Text(r.src), "\"`"), Line: extract.Line(n)}
	if name := n.ChildByField("name"); name != nil {
		imp.Alias = name.Text(r.src)
	}
	r.res.Imports = append(r.res.Imports, imp)
}

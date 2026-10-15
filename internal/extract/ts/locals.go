package ts

import (
	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/parse"
)

// declarations are the node kinds that declare variables or class
// properties: visit records them once the references inside them are
// collected, so an initializer still sees an earlier variable of the
// same name.
var declarations = map[string]bool{
	"variable_declarator": true, "required_parameter": true, "optional_parameter": true,
	"formal_parameters": true, "public_field_definition": true, "field_definition": true,
	"assignment_expression": true,
}

// scopes are the node kinds that open a lexical scope (see
// extract.Locals): functions, methods and arrow functions, blocks,
// loops with their own variables, and catch clauses.
var scopes = map[string]bool{
	"function_declaration": true, "function_expression": true, "function": true, "arrow_function": true,
	"generator_function": true, "generator_function_declaration": true, "method_definition": true,
	"statement_block": true, "switch_body": true, "class_static_block": true,
	"for_statement": true, "for_in_statement": true, "catch_clause": true,
}

// callables are the scopes whose parameters are variables of a body;
// those of a function type or of a signature declare nothing.
var callables = map[string]bool{
	"function_declaration": true, "function_expression": true, "function": true, "arrow_function": true,
	"generator_function": true, "generator_function_declaration": true, "method_definition": true,
}

// enter declares what a scope binds before its children are visited:
// the bare parameter of an arrow function (x => ...), the variable of a
// for...of or for...in loop and that of a catch clause. The variable of
// for...of has the element type of what the loop goes over, where that
// is evident (see typeOf); the others have no type codedoc follows and
// only hide an outer variable of their name.
func (r *refWalker) enter(n parse.Node) {
	switch n.Kind() {
	case "arrow_function":
		r.bind(n.ChildByField("parameter"), "")
	case "for_in_statement":
		elem := ""
		if op := n.ChildByField("operator"); op != nil && op.Kind() == "of" {
			elem = extract.ElemOf(r.typeOf(n.ChildByField("right")))
		}
		r.bind(n.ChildByField("left"), elem)
	case "catch_clause":
		r.bind(n.ChildByField("parameter"), "")
	}
}

// declare records the type of the variable or class property a
// declaration introduces (see extract.Locals): the class it is
// constructed from (new T(...)), else its type annotation, else what
// its value makes evident (see typeOf). Any other declaration of a
// variable hides the name.
func (r *refWalker) declare(n parse.Node) {
	switch n.Kind() {
	case "variable_declarator":
		r.variable(n.ChildByField("name"), n.ChildByField("type"), n.ChildByField("value"))
	case "required_parameter", "optional_parameter":
		if ownsBody(n.Parent()) {
			r.variable(n.ChildByField("pattern"), n.ChildByField("type"), nil)
			r.parameterProperty(n)
		}
	case "formal_parameters":
		if ownsBody(n) {
			for _, p := range extract.NamedChildren(n, "identifier") { // untyped JS parameters
				r.bind(p, "")
			}
		}
	default:
		r.declareProperty(n)
	}
}

// ownsBody reports whether a parameter list is that of a function with
// a body (see callables).
func ownsBody(params parse.Node) bool {
	return params != nil && params.Parent() != nil && callables[params.Parent().Kind()]
}

// declareTop declares the variables of the file's top-level
// declarations, exported or not, before the walk: a function sees the
// module-level variables declared below it.
func (r *refWalker) declareTop(root parse.Node) {
	for i := uint(0); i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		if n.Kind() == "export_statement" {
			if n = n.ChildByField("declaration"); n == nil {
				continue
			}
		}
		if k := n.Kind(); k != "lexical_declaration" && k != "variable_declaration" {
			continue
		}
		for _, d := range extract.NamedChildren(n, "variable_declarator") {
			r.declare(d)
		}
	}
}

// declareProperty is declare for the declarations of class properties,
// a field definition and an assignment to this.name, and for the
// assignment of a variable (see reassign).
func (r *refWalker) declareProperty(n parse.Node) {
	switch n.Kind() {
	case "public_field_definition", "field_definition":
		name := n.ChildByField("name")
		if name == nil {
			name = n.ChildByField("property")
		}
		r.property(name, n.ChildByField("type") != nil, n.ChildByField("value"))
	case "assignment_expression":
		left := n.ChildByField("left")
		switch {
		case left == nil:
		case left.Kind() == "identifier":
			r.reassign(left.Text(r.src), n.ChildByField("right"))
		case left.Kind() == "member_expression":
			if obj := left.ChildByField("object"); obj != nil && obj.Kind() == "this" {
				r.property(left.ChildByField("property"), false, n.ChildByField("right"))
			}
		}
	}
}

// reassign makes a variable untyped from here on when it is assigned an
// object of another class than the one recorded for it (let z: A | B =
// new A(); z = new B()): its declaration no longer tells what it holds.
func (r *refWalker) reassign(name string, value parse.Node) {
	typ, ok := r.locals.Lookup(name)
	if class := r.constructed(value); ok && class != "" && class != typ {
		r.locals.Retype(name, "")
	}
}

// variable declares a local variable. A variable bound to a required
// module is the alias of an import, not a variable.
func (r *refWalker) variable(name, annotation, value parse.Node) {
	if name == nil || r.isRequire(value) {
		return
	}
	typ := ""
	if name.Kind() == "identifier" {
		typ = r.constructed(value)
	}
	switch {
	case typ != "":
	case annotation != nil:
		typ = r.annotated(annotation)
	default:
		typ = r.typeOf(value)
	}
	r.bind(name, typ)
}

// bind declares the names a binding introduces: an identifier has the
// type typ; an object pattern ({ a, b: c }) gives each name it binds
// the declared type of the property it takes from typ (see
// bindProperty); the names of an array or rest pattern have no evident
// type.
func (r *refWalker) bind(n parse.Node, typ string) {
	if n == nil {
		return
	}
	switch n.Kind() {
	case "identifier":
		r.locals.Declare(n.Text(r.src), typ)
	case "assignment_pattern":
		r.bind(n.ChildByField("left"), typ)
	case "object_pattern":
		for i := uint(0); i < n.NamedChildCount(); i++ {
			r.bindProperty(n.NamedChild(i), typ)
		}
	case "array_pattern", "rest_pattern":
		for i := uint(0); i < n.NamedChildCount(); i++ {
			r.bind(n.NamedChild(i), "")
		}
	}
}

// bindProperty declares what one entry of an object pattern binds from
// an object of type obj: { a } and { a = 1 } the property a, { a: b }
// the property a under the name or pattern b, { ...rest } a name
// without a type.
func (r *refWalker) bindProperty(n parse.Node, obj string) {
	switch n.Kind() {
	case "shorthand_property_identifier_pattern":
		name := n.Text(r.src)
		r.locals.Declare(name, extract.FieldOf(obj, name))
	case "object_assignment_pattern":
		r.bindProperty(n.ChildByField("left"), obj)
	case "pair_pattern":
		typ := ""
		if key := n.ChildByField("key"); key != nil && key.Kind() == "property_identifier" {
			typ = extract.FieldOf(obj, key.Text(r.src))
		}
		r.bind(n.ChildByField("value"), typ)
	default:
		r.bind(n, "")
	}
}

// parameterProperty declares the class property a constructor
// parameter with an accessibility modifier or readonly introduces
// (constructor(private store: Store)), with its annotated type.
func (r *refWalker) parameterProperty(n parse.Node) {
	name := n.ChildByField("pattern")
	if name == nil || name.Kind() != "identifier" || !declaresProperty(n) {
		return
	}
	member, prop := r.enc.At(name.StartByte()), name.Text(r.src)
	if !r.locals.PropertyDeclared(member, prop) {
		r.locals.DeclareProperty(member, prop, r.annotated(n.ChildByField("type")))
	}
}

// declaresProperty reports whether a parameter carries a modifier that
// makes it a property of its class.
func declaresProperty(param parse.Node) bool {
	for i := uint(0); i < param.ChildCount(); i++ {
		if k := param.Child(i).Kind(); k == "accessibility_modifier" || k == "readonly" {
			return true
		}
	}
	return false
}

// isRequire reports whether a value is require('x') with a literal
// argument: requireOrImport records it as an import.
func (r *refWalker) isRequire(value parse.Node) bool {
	if value == nil || value.Kind() != "call_expression" {
		return false
	}
	fn := value.ChildByField("function")
	return fn != nil && fn.Kind() == "identifier" && fn.Text(r.src) == "require" && r.literalArg(value) != ""
}

// property declares this.name for the enclosing class. The first
// declaration or assignment in the class decides: a property with a
// type annotation is left to the resolver, which reads the annotation
// from the field's signature, so nothing later overrides it.
func (r *refWalker) property(name parse.Node, annotated bool, value parse.Node) {
	if name == nil || name.Kind() != "property_identifier" {
		return
	}
	member, prop := r.enc.At(name.StartByte()), name.Text(r.src)
	if r.locals.PropertyDeclared(member, prop) {
		return
	}
	typ := ""
	if !annotated {
		typ = r.constructed(value)
	}
	r.locals.DeclareProperty(member, prop, typ)
}

// constructed returns the class a value is constructed from: T for
// new T(...), ns.T for new ns.T(...); empty for any other value.
func (r *refWalker) constructed(value parse.Node) string {
	if value == nil || value.Kind() != "new_expression" {
		return ""
	}
	return r.plainName(value.ChildByField("constructor"), "identifier", "member_expression")
}

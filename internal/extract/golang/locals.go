package golang

import (
	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/parse"
)

// declarations are the node kinds that declare local variables, and
// the types a function declares. visit records most of them once the
// references inside them are collected, so the right-hand side of x :=
// x.Next() still sees the earlier x; the alias of a type switch, whose
// cases lie inside the node, comes first (true).
var declarations = map[string]bool{
	"type_spec": false, "type_alias": false,
	"short_var_declaration": false, "var_spec": false, "const_spec": false, "parameter_declaration": false,
	"variadic_parameter_declaration": false, "range_clause": false, "receive_statement": false,
	"type_switch_statement": true,
}

// scopes are the node kinds that open a lexical scope (see
// extract.Locals): functions and function literals, blocks, the
// statements whose initializer declares variables for their body, and
// the clauses of a switch or select, each a block of its own.
var scopes = map[string]bool{
	"function_declaration": true, "method_declaration": true, "func_literal": true, "block": true,
	"if_statement": true, "for_statement": true, "expression_switch_statement": true, "type_switch_statement": true,
	"select_statement": true, "expression_case": true, "default_case": true, "type_case": true,
	"communication_case": true,
}

// callables are the node kinds whose parameters and results are
// variables of a body; those of a function type or of an interface
// method declare nothing.
var callables = map[string]bool{"function_declaration": true, "method_declaration": true, "func_literal": true}

// declare records the types of the variables a declaration introduces
// (see extract.Locals): a declared type, or what the initializer makes
// evident (see valueType). Any other declaration hides the name.
func (r *refWalker) declare(n parse.Node) {
	switch n.Kind() {
	case "short_var_declaration":
		r.assign(names(n.ChildByField("left")), n.ChildByField("right"))
	case "var_spec", "const_spec":
		if typ := n.ChildByField("type"); typ != nil {
			r.declareAll(extract.NamedChildren(n, "identifier"), r.typeName(typ))
			return
		}
		r.assign(extract.NamedChildren(n, "identifier"), n.ChildByField("value"))
	case "parameter_declaration", "variadic_parameter_declaration":
		r.parameter(n)
	case "range_clause":
		r.ranged(names(n.ChildByField("left")), n.ChildByField("right"))
	case "receive_statement":
		if declaresReceived(n) {
			r.declareAll(names(n.ChildByField("left")), "")
		}
	case "type_switch_statement":
		r.declareAll(names(n.ChildByField("alias")), "")
	case "type_spec", "type_alias":
		r.localType(n)
	}
}

// localType declares the name of a type declared inside a function: no
// symbol declares the members of such a type, so a variable of it has a
// type the index does not hold (see typeName).
func (r *refWalker) localType(spec parse.Node) {
	if name := spec.ChildByField("name"); name != nil && r.locals.Nested() {
		r.locals.Declare(name.Text(r.src), extract.BuiltinType)
	}
}

// parameter declares the parameters and named results of a function
// with a body; the receiver of a method is left to the resolver, which
// reads it from the method's signature. A variadic parameter is a
// slice, whose type no symbol declares.
func (r *refWalker) parameter(n parse.Node) {
	list := n.Parent()
	if list == nil || list.Parent() == nil || !callables[list.Parent().Kind()] || isReceiver(n) {
		return
	}
	typ := ""
	if n.Kind() == "parameter_declaration" {
		typ = r.typeName(n.ChildByField("type"))
	}
	r.declareAll(extract.NamedChildren(n, "identifier"), typ)
}

// declaresReceived reports whether the receive statement of a select
// case declares its variables (v := <-ch) rather than assigning them.
func declaresReceived(n parse.Node) bool {
	for i := uint(0); i < n.ChildCount(); i++ {
		if n.Child(i).Kind() == ":=" {
			return true
		}
	}
	return false
}

// declareTop declares the variables and constants of the file's
// top-level declarations before the walk, so a function sees those
// declared below it.
func (r *refWalker) declareTop(root parse.Node) {
	for i := uint(0); i < root.NamedChildCount(); i++ {
		decl := root.NamedChild(i)
		if k := decl.Kind(); k != "var_declaration" && k != "const_declaration" {
			continue
		}
		for j := uint(0); j < decl.NamedChildCount(); j++ {
			spec := decl.NamedChild(j)
			if spec.Kind() == "var_spec_list" {
				for _, s := range extract.NamedChildren(spec, "var_spec") {
					r.declare(s)
				}
				continue
			}
			r.declare(spec)
		}
	}
}

// names returns the identifiers of an expression list.
func names(list parse.Node) []parse.Node {
	if list == nil {
		return nil
	}
	return extract.NamedChildren(list, "identifier")
}

// isReceiver reports whether a parameter declaration is the receiver of
// a method, which the resolver reads from the method's signature.
func isReceiver(n parse.Node) bool {
	list := n.Parent()
	return list != nil && list.Parent() != nil && extract.IsField(list.Parent(), "receiver", list)
}

func (r *refWalker) declareAll(vars []parse.Node, typ string) {
	for _, v := range vars {
		r.locals.Declare(v.Text(r.src), typ)
	}
}

// assign declares vars from the values they are initialized with, one
// value per variable; a single call initializing several variables
// gives the first one its first result.
func (r *refWalker) assign(vars []parse.Node, values parse.Node) {
	if values == nil || len(vars) == 0 {
		r.declareAll(vars, "")
		return
	}
	types := make([]string, len(vars))
	n := values.NamedChildCount()
	if n == 1 && len(vars) > 1 {
		if v := values.NamedChild(0); v.Kind() == "call_expression" {
			types[0] = r.valueType(v)
		}
	}
	for i := uint(0); i < n && int(n) == len(vars); i++ {
		types[i] = r.valueType(values.NamedChild(i))
	}
	for i, v := range vars {
		r.declareAll([]parse.Node{v}, types[i])
	}
}

// ranged declares the variables of a range clause: the second one has
// the element type of the slice, array or map ranged over, where that
// is evident (see exprType); the first is an index or a key.
func (r *refWalker) ranged(vars []parse.Node, over parse.Node) {
	r.declareAll(vars, "")
	if len(vars) == 2 {
		r.locals.Declare(vars[1].Text(r.src), extract.ElemOf(r.exprType(over)))
	}
}

// valueType returns the type an initializer makes evident: T for T{...},
// &T{...}, new(T) and x.(T), the type of the variable it copies, the
// declared type of a field of a typed value (x.f, a type path: see
// extract.FieldOf), and for a call what callType says. It is empty for
// anything else.
func (r *refWalker) valueType(v parse.Node) string {
	switch v.Kind() {
	case "identifier":
		typ, _ := r.locals.Lookup(v.Text(r.src))
		return typ
	case "type_assertion_expression":
		return r.typeName(v.ChildByField("type"))
	case "composite_literal":
		return r.typeName(v.ChildByField("type"))
	case "unary_expression":
		if op := v.ChildByField("operand"); op != nil && op.Kind() == "composite_literal" && v.Text(r.src)[0] == '&' {
			return r.valueType(op)
		}
	case "selector_expression":
		return r.exprType(v)
	case "call_expression":
		return r.callType(v)
	}
	return ""
}

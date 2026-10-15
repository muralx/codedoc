package ts

import (
	"strings"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/parse"
)

// walker collects the records of one file.
type walker struct {
	src         []byte
	prefix      string // file path plus "/", the qualified-name prefix
	ns          string // enclosing namespaces joined with dots, plus a dot; "" at top level
	testFile    bool
	moduleDocAt int // start byte of the module doc comment, -1 when none
	tests       []testRef
	res         *extract.FileResult
	// enums holds, by enum, the text of its members (they are no
	// symbols), which takes part in its body hash.
	enums map[string]string
	// api is set in API mode (see ExtractAPI): the names the file exports
	// apart from their declaration.
	api *exportSet
}

// walk visits the statements of a file or namespace body. Declarations
// inside function bodies are local and not indexed.
func (w *walker) walk(list parse.Node, exported bool) {
	for _, d := range w.decls(list, statementKey) {
		w.statement(d.last, d, exported || w.api.has(w.declName(d.last)))
	}
}

// statement handles one statement. d carries the doc comment and the span:
// the export statement or ambient declaration that wraps n, if any.
// exported is true when the declaration is visible outside its file.
func (w *walker) statement(n parse.Node, d decl, exported bool) {
	switch n.Kind() {
	case "export_statement":
		if inner := n.ChildByField("declaration"); inner != nil {
			w.statement(inner, d, exported || w.ns == "")
		}
	case "ambient_declaration":
		if n.NamedChildCount() > 0 {
			w.statement(n.NamedChild(0), d, exported)
		}
	case "class_declaration", "abstract_class_declaration":
		w.class(n, d, exported)
	case "interface_declaration":
		w.iface(n, d, exported)
	case "lexical_declaration", "variable_declaration":
		w.variables(n, d, exported)
	case "internal_module", "module":
		w.namespace(n, exported)
	case "expression_statement":
		w.expression(n, d, exported)
	default:
		w.leaf(n, d, exported)
	}
}

// leaf adds a function, function signature, type alias (with the
// members of an object type: see objectAlias) or enum; other statements
// are ignored.
func (w *walker) leaf(n parse.Node, d decl, exported bool) {
	kind := extract.KindFunction
	switch n.Kind() {
	case "function_declaration", "generator_function_declaration", "function_signature":
	case "type_alias_declaration":
		kind = extract.KindTypeAlias
	case "enum_declaration":
		kind = extract.KindEnum
	default:
		return
	}
	s := w.symbol(kind, w.name(n), "", d, n, topVisibility(exported))
	w.withBody(&s, d, n.ChildByField("body"))
	if kind == extract.KindEnum {
		w.enumMembers(s.Qualified, n.ChildByField("body"))
	}
	w.add(s)
	if kind == extract.KindTypeAlias {
		w.objectAlias(n, s.Qualified, exported)
	}
}

// enumMembers records the text of an enum's members, comments left out.
func (w *walker) enumMembers(qualified string, body parse.Node) {
	if body == nil {
		return
	}
	var members []string
	for i := uint(0); i < body.NamedChildCount(); i++ {
		if c := body.NamedChild(i); c.Kind() != "comment" {
			members = append(members, c.Text(w.src))
		}
	}
	if w.enums == nil {
		w.enums = map[string]string{}
	}
	w.enums[qualified] = strings.Join(members, "\n")
}

// expression handles a namespace declaration, which the grammar wraps in
// an expression statement, and test calls in test files.
func (w *walker) expression(n parse.Node, d decl, exported bool) {
	if n.NamedChildCount() > 0 && n.NamedChild(0).Kind() == "internal_module" {
		w.statement(n.NamedChild(0), d, exported)
		return
	}
	if w.testFile && w.ns == "" {
		w.testCalls(n, nil)
	}
}

// namespace walks the body of namespace NS {} or declare module "x" {}.
// Members are qualified NS.member; no symbol is created for the namespace
// itself (there is no namespace kind), so a module without declarations
// contributes nothing. A member is public when its namespace is public and
// it is exported, explicitly or implicitly as in every ambient namespace.
// A quoted module (declare module "x") is public.
func (w *walker) namespace(n parse.Node, exported bool) {
	body, name := n.ChildByField("body"), n.ChildByField("name")
	if body == nil || name == nil {
		return
	}
	implicit := n.Parent() != nil && n.Parent().Kind() == "ambient_declaration"
	if name.Kind() == "string" {
		exported = true
	}
	outer := w.ns
	w.ns = outer + w.name(n) + "."
	for _, d := range w.decls(body, statementKey) {
		w.statement(d.last, d, exported && (implicit || d.last.Kind() == "export_statement"))
	}
	w.ns = outer
}

// variables adds one symbol per declarator with a plain identifier name.
// Function-valued declarators are functions; const is const, let and var
// are var. With several declarators the first one's signature starts at
// the declaration keyword and later ones get the keyword prefixed to their
// own text. Class expressions (const C = class { ... }) give one const or
// var symbol; their members are not extracted in v1.
func (w *walker) variables(n parse.Node, d decl, exported bool) {
	decls := extract.NamedChildren(n, "variable_declarator")
	kind := extract.KindVar
	if n.Child(0) != nil && n.Child(0).Kind() == "const" {
		kind = extract.KindConst
	}
	for i, v := range decls {
		name := v.ChildByField("name")
		if name == nil || name.Kind() != "identifier" {
			continue
		}
		from := d
		if len(decls) > 1 {
			from.last = v
			if i > 0 {
				from = single(v)
			}
		}
		k, body := kind, parse.Node(nil)
		if value := v.ChildByField("value"); value != nil &&
			(value.Kind() == "arrow_function" || value.Kind() == "function_expression") {
			k, body = extract.KindFunction, value.ChildByField("body")
		}
		s := w.symbol(k, name.Text(w.src), "", from, v, topVisibility(exported))
		w.withBody(&s, from, body)
		s.Value = w.constValue(k, v)
		if i > 0 {
			keyword := extract.Collapse(string(w.src[d.sig.StartByte():n.Child(0).EndByte()]))
			s.SigText = keyword + " " + s.SigText
		}
		w.add(s)
	}
}

// constValue returns the string literal a const declarator is
// initialized with, "" for other kinds and initializers.
func (w *walker) constValue(kind extract.Kind, v parse.Node) string {
	if kind != extract.KindConst {
		return ""
	}
	value, _ := stringLiteral(v.ChildByField("value"), w.src)
	return value
}

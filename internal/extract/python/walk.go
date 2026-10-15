package python

import (
	"strings"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/parse"
)

// walker collects the records of one Python file.
type walker struct {
	src      []byte
	testFile bool
	res      *extract.FileResult
	// instance marks the symbols of self.x fields, which sit inside
	// __init__ and must not enclose the references there.
	instance map[int]bool
}

// scope is the module or class whose body is being walked.
type scope struct {
	container string          // qualified name of the class, "" at module level
	class     string          // simple name of the class, "" at module level
	fields    map[string]bool // variable or field names already declared here
}

// compound are the statements whose blocks run in the enclosing scope,
// and the clauses that hold their alternative blocks.
var compound = map[string]bool{
	"if_statement": true, "elif_clause": true, "else_clause": true, "try_statement": true,
	"except_clause": true, "except_group_clause": true, "finally_clause": true, "with_statement": true,
}

// block visits the statements of a module or class body, including those
// nested in if, try and with statements, which define names in the same
// scope. Definitions inside function bodies are local and not indexed.
func (w *walker) block(body parse.Node, sc scope) {
	for i := uint(0); i < body.NamedChildCount(); i++ {
		switch c := body.NamedChild(i); c.Kind() {
		case "block":
			w.block(c, sc)
		case "decorated_definition":
			if def := c.ChildByField("definition"); def != nil {
				w.definition(def, c, sc)
			}
		case "function_definition", "class_definition":
			w.definition(c, c, sc)
		case "expression_statement":
			w.assignment(c, sc)
		default:
			if compound[c.Kind()] {
				w.block(c, sc)
			}
		}
	}
}

// definition adds a class or function n; outer is n itself or the
// decorated definition wrapping it, where the span starts.
func (w *walker) definition(n, outer parse.Node, sc scope) {
	name := n.ChildByField("name")
	if name == nil {
		return
	}
	if n.Kind() == "class_definition" {
		s := w.symbol(extract.KindClass, name.Text(w.src), sc, n, outer)
		w.add(s)
		if body := n.ChildByField("body"); body != nil {
			w.block(body, scope{container: s.Qualified, class: s.Name, fields: map[string]bool{}})
		}
		return
	}
	kind := extract.KindFunction
	if sc.container != "" {
		kind = extract.KindMethod
	}
	s := w.symbol(kind, name.Text(w.src), sc, n, outer)
	s.IsTest = w.testFile && strings.HasPrefix(s.Name, "test_") &&
		(sc.container == "" || strings.HasPrefix(sc.class, "Test"))
	w.add(s)
	if sc.container != "" && s.Name == "__init__" {
		w.instanceFields(n, sc)
	}
}

// symbol fills the fields a class or function shares. The signature runs
// from def or class to the colon that opens the body; the body hash and
// doc come from the body block (the hash covers the signature text and the
// body after the docstring).
func (w *walker) symbol(kind extract.Kind, name string, sc scope, n, outer parse.Node) extract.Symbol {
	s := extract.Symbol{
		Kind: kind, Name: name, Qualified: qualify(sc, name), Container: sc.container,
		Span: extract.SpanOf(outer), Visibility: visibility(name), BodyHash: extract.NodeHash(w.src, n),
	}
	sigEnd := n.EndByte()
	if body := n.ChildByField("body"); body != nil {
		sigEnd = colonBefore(n, body)
		from := body.StartByte()
		if d := docstring(body); d != nil {
			s.Doc, from = w.doc(d), d.EndByte()
		}
		s.Signature, s.SigText = extract.SignatureSpan(n, sigEnd, w.src)
		s.BodyHash = extract.CodeHash(s.SigText, w.src, body, from, body.EndByte())
		return s
	}
	s.Signature, s.SigText = extract.SignatureSpan(n, sigEnd, w.src)
	return s
}

// colonBefore returns the offset of the colon that opens body, or the
// body's start when the colon is missing.
func colonBefore(n, body parse.Node) uint {
	end := body.StartByte()
	for i := uint(0); i < n.ChildCount(); i++ {
		if c := n.Child(i); c.Kind() == ":" && c.EndByte() <= body.StartByte() {
			end = c.StartByte()
		}
	}
	return end
}

// assignment adds the name assigned by a NAME = value statement: a const
// (ALL_CAPS) or var at module level, a field in a class body. Only the
// first assignment of a name declares it.
func (w *walker) assignment(stmt parse.Node, sc scope) {
	if stmt.NamedChildCount() == 0 || stmt.NamedChild(0).Kind() != "assignment" {
		return
	}
	left := stmt.NamedChild(0).ChildByField("left")
	if left == nil || left.Kind() != "identifier" {
		return
	}
	name := left.Text(w.src)
	kind := extract.KindField
	if sc.container == "" {
		kind = extract.KindVar
		if isConstName(name) {
			kind = extract.KindConst
		}
	}
	if w.declare(kind, name, stmt, sc) && isConstName(name) {
		w.res.Symbols[len(w.res.Symbols)-1].Value, _ = stringLiteral(stmt.NamedChild(0).ChildByField("right"), w.src)
	}
}

// instanceFields adds a field for every self.<name> assignment in the body
// of __init__, outside nested functions and classes. self is the method's
// first parameter, whatever its name.
func (w *walker) instanceFields(init parse.Node, sc scope) {
	params, body := init.ChildByField("parameters"), init.ChildByField("body")
	if params == nil || body == nil || params.NamedChildCount() == 0 || params.NamedChild(0).Kind() != "identifier" {
		return
	}
	w.selfAssignments(body, params.NamedChild(0).Text(w.src), sc)
}

func (w *walker) selfAssignments(n parse.Node, self string, sc scope) {
	for i := uint(0); i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		switch c.Kind() {
		case "function_definition", "class_definition", "decorated_definition", "lambda":
			continue
		case "assignment":
			if left := c.ChildByField("left"); left != nil && left.Kind() == "attribute" {
				obj, attr := left.ChildByField("object"), left.ChildByField("attribute")
				if obj != nil && attr != nil && obj.Kind() == "identifier" && obj.Text(w.src) == self {
					stmt := c
					if c.Parent().Kind() == "expression_statement" {
						stmt = c.Parent()
					}
					if w.declare(extract.KindField, attr.Text(w.src), stmt, sc) {
						w.instance[len(w.res.Symbols)-1] = true
					}
				}
			}
		}
		w.selfAssignments(c, self, sc)
	}
}

// declare adds a variable or field declared by stmt unless the scope
// already declared name, and reports whether it added one. The signature
// is the statement's first line.
func (w *walker) declare(kind extract.Kind, name string, stmt parse.Node, sc scope) bool {
	if sc.fields[name] {
		return false
	}
	sc.fields[name] = true
	s := extract.Symbol{
		Kind: kind, Name: name, Qualified: qualify(sc, name), Container: sc.container,
		Span: extract.SpanOf(stmt), Visibility: visibility(name), BodyHash: extract.NodeHash(w.src, stmt),
	}
	s.Signature, s.SigText = extract.SignatureSpan(stmt, extract.LineEnd(stmt, w.src), w.src)
	w.add(s)
	return true
}

func (w *walker) add(s extract.Symbol) { w.res.Symbols = append(w.res.Symbols, s) }

func qualify(sc scope, name string) string {
	if sc.container == "" {
		return name
	}
	return sc.container + "." + name
}

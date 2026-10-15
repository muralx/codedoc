package ts

import "github.com/muralx/codedoc/internal/parse"

// decl is one declaration with the siblings that belong to it: decorators
// before it and, for overloads and accessor pairs, the earlier siblings of
// the same name.
type decl struct {
	first parse.Node // first sibling of the group: span start and doc anchor
	sig   parse.Node // where the signature starts: last's first decorator, or last
	last  parse.Node // the implementation or last signature; ends the span
}

// single is a declaration made of one node.
func single(n parse.Node) decl { return decl{first: n, sig: n, last: n} }

// grouped reports whether d spans earlier overloads or accessors.
func (d decl) grouped() bool { return d.first.StartByte() != d.sig.StartByte() }

// decls splits the named children of a statement list, class body or
// interface body into declarations. Comments are skipped (docs are found
// from the first sibling), decorators attach to the following member, and
// adjacent members with the same non-empty key collapse into one: overload
// signatures followed by their implementation, or a get/set pair.
func (w *walker) decls(list parse.Node, key func(*walker, parse.Node) string) []decl {
	var out []decl
	var decorator parse.Node
	prevKey := ""
	for i := uint(0); i < list.NamedChildCount(); i++ {
		c := list.NamedChild(i)
		switch c.Kind() {
		case "comment":
			continue
		case "decorator":
			if decorator == nil {
				decorator = c
			}
			continue
		}
		sig := c
		if decorator != nil {
			sig, decorator = decorator, nil
		}
		k := key(w, c)
		if k != "" && k == prevKey {
			out[len(out)-1].sig, out[len(out)-1].last = sig, c
		} else {
			out = append(out, decl{first: sig, sig: sig, last: c})
		}
		prevKey = k
	}
	return out
}

// statementKey groups top-level function overloads: function signatures
// and the function declaration of the same name, exported or ambient.
func statementKey(w *walker, n parse.Node) string {
	n = unwrap(n)
	if k := n.Kind(); k == "function_signature" || k == "function_declaration" {
		return w.name(n)
	}
	return ""
}

// memberKey groups class method overloads and accessor pairs.
func memberKey(w *walker, n parse.Node) string {
	switch n.Kind() {
	case "method_definition", "method_signature", "abstract_method_signature":
		return w.name(n)
	}
	return ""
}

// interfaceKey groups interface method overloads.
func interfaceKey(w *walker, n parse.Node) string {
	if n.Kind() == "method_signature" {
		return w.name(n)
	}
	return ""
}

// unwrap returns the declaration inside an export statement, ambient
// declaration or namespace expression statement, or n itself.
func unwrap(n parse.Node) parse.Node {
	for {
		var inner parse.Node
		switch n.Kind() {
		case "export_statement":
			inner = n.ChildByField("declaration")
		case "ambient_declaration", "expression_statement":
			if n.NamedChildCount() > 0 {
				inner = n.NamedChild(0)
			}
		}
		if inner == nil {
			return n
		}
		n = inner
	}
}

// isDeclaration reports whether a top-level statement declares something a
// doc comment can document.
func isDeclaration(n parse.Node) bool {
	switch unwrap(n).Kind() {
	case "function_declaration", "generator_function_declaration", "function_signature",
		"class_declaration", "abstract_class_declaration", "interface_declaration",
		"type_alias_declaration", "enum_declaration", "lexical_declaration",
		"variable_declaration", "internal_module", "module":
		return true
	}
	return false
}

package ts

import (
	"strings"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/parse"
)

// importStatement records import x, { a, b as c }, * as ns from 'source'.
// Names lists the imported names as the source exports them, one
// imported under another name as "b=c"; Alias is the default or
// namespace binding.
func (r *refWalker) importStatement(n parse.Node) {
	imp, ok := r.source(n)
	if !ok {
		return
	}
	for _, clause := range extract.NamedChildren(n, "import_clause") {
		for i := uint(0); i < clause.NamedChildCount(); i++ {
			switch c := clause.NamedChild(i); c.Kind() {
			case "identifier":
				imp.Alias = c.Text(r.src)
			case "namespace_import":
				if c.NamedChildCount() > 0 {
					imp.Alias = c.NamedChild(0).Text(r.src)
				}
			case "named_imports":
				imp.Names = r.specifierNames(c, "import_specifier")
			}
		}
	}
	r.res.Imports = append(r.res.Imports, imp)
}

// reexport records export * from 'x', export * as ns from 'x' and
// export { a, b as c } from 'x' as imports of the source, marked as
// re-exports. Names is nil for a star export.
func (r *refWalker) reexport(n parse.Node) {
	imp, ok := r.source(n)
	if !ok {
		return
	}
	imp.Reexport = true
	for i := uint(0); i < n.NamedChildCount(); i++ {
		switch c := n.NamedChild(i); c.Kind() {
		case "export_clause":
			imp.Names = r.specifierNames(c, "export_specifier")
		case "namespace_export":
			if c.NamedChildCount() > 0 {
				imp.Alias = c.NamedChild(0).Text(r.src)
			}
		}
	}
	r.res.Imports = append(r.res.Imports, imp)
}

// source returns an import with the unquoted source string of n, or false
// when n has none.
func (r *refWalker) source(n parse.Node) (extract.Import, bool) {
	src := n.ChildByField("source")
	if src == nil {
		return extract.Import{}, false
	}
	return extract.Import{Raw: strings.Trim(src.Text(r.src), "'\"`"), Line: extract.Line(n)}, true
}

// specifierNames returns the source-side name of every specifier in
// list; a specifier bound or exported under another name (b as c) is
// "b=c".
func (r *refWalker) specifierNames(list parse.Node, kind string) []string {
	var names []string
	for _, s := range extract.NamedChildren(list, kind) {
		name := s.ChildByField("name")
		if name == nil {
			continue
		}
		text := strings.Trim(name.Text(r.src), "'\"")
		if alias := s.ChildByField("alias"); alias != nil && alias.Text(r.src) != text {
			text += "=" + alias.Text(r.src)
		}
		names = append(names, text)
	}
	return names
}

// requireOrImport records require('x') and dynamic import('x') with a
// string literal argument as imports of x, and reports whether n was one.
// The alias is the variable a plain require is assigned to.
func (r *refWalker) requireOrImport(n parse.Node) bool {
	fn, arg := n.ChildByField("function"), r.literalArg(n)
	if fn == nil || arg == "" {
		return false
	}
	isRequire := fn.Kind() == "identifier" && fn.Text(r.src) == "require"
	if !isRequire && fn.Kind() != "import" {
		return false
	}
	imp := extract.Import{Raw: arg, Line: extract.Line(n)}
	if p := n.Parent(); isRequire && p != nil && p.Kind() == "variable_declarator" {
		if name := p.ChildByField("name"); name != nil && name.Kind() == "identifier" {
			imp.Alias = name.Text(r.src)
		}
	}
	r.res.Imports = append(r.res.Imports, imp)
	return true
}

// literalArg returns the unquoted text of a call's single string literal
// argument, or "" when the call has another argument list.
func (r *refWalker) literalArg(call parse.Node) string {
	args := call.ChildByField("arguments")
	if args == nil || args.Kind() != "arguments" || extract.ArgCount(args) != 1 {
		return ""
	}
	if arg := args.NamedChild(0); arg.Kind() == "string" {
		return strings.Trim(arg.Text(r.src), "'\"")
	}
	return ""
}

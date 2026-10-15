package ts

import (
	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/parse"
)

// ExtractAPI parses a file of a third-party package, usually a
// declaration file, and returns its public API only: the declarations
// the file exports, whether at the declaration (export class C), in an
// export clause (export { C }), as the default export or through
// export =, with their public and protected members. A file without any
// top-level import or export is a global script, all of whose
// declarations are public. The imports and re-exports of the file are
// returned so that the caller can follow them. Of the references only
// the supertypes of the kept declarations are returned (see supertypes).
func (e *Extractor) ExtractAPI(filePath string, src []byte) (*extract.FileResult, error) {
	tree, err := parse.Parse(e.lang, src)
	if err != nil {
		return nil, err
	}
	defer tree.Close()
	root := tree.Root()
	w := &walker{src: src, prefix: filePath + "/", moduleDocAt: -1, res: &extract.FileResult{}}
	w.api = w.exports(root)
	if isIndex(filePath) {
		w.moduleDoc(root)
	}
	w.walk(root, w.api.all)
	extract.HashContainers(w.res.Symbols, w.enums)
	extract.UniqueQualified(w.res.Symbols)
	w.res.Symbols = apiSymbols(w.res.Symbols)
	w.links(root)
	w.supertypes(root)
	w.res.ParseErrors = extract.CountErrors(root)
	return w.res, nil
}

// exportSet holds what a file exports apart from its export declarations.
type exportSet struct {
	// all is set for a global script: a file without top-level imports
	// and exports, whose every declaration is visible.
	all bool
	// names are the local names of export clauses, of the default export
	// and of export =.
	names map[string]bool
}

// has reports whether a top-level name is exported by name; a nil set
// (the ordinary extraction) exports nothing that way.
func (x *exportSet) has(name string) bool { return x != nil && name != "" && x.names[name] }

// exports reads the top-level statements of a file for the names it
// exports by name and for whether it is a module at all.
func (w *walker) exports(root parse.Node) *exportSet {
	x := &exportSet{all: true, names: map[string]bool{}}
	for i := uint(0); i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		switch n.Kind() {
		case "import_statement":
			x.all = false
		case "export_statement":
			x.all = false
			if n.ChildByField("source") == nil {
				w.exportedNames(n, x.names)
			}
		}
	}
	return x
}

// exportedNames adds the local names an export statement without a
// source exports: export { a, b as c }, export default a and export = a.
func (w *walker) exportedNames(n parse.Node, names map[string]bool) {
	for i := uint(0); i < n.NamedChildCount(); i++ {
		switch c := n.NamedChild(i); c.Kind() {
		case "identifier":
			names[c.Text(w.src)] = true
		case "export_clause":
			for _, spec := range extract.NamedChildren(c, "export_specifier") {
				if name := spec.ChildByField("name"); name != nil {
					names[name.Text(w.src)] = true
				}
			}
		}
	}
}

// declName returns the name a top-level statement declares: that of the
// declaration, of the first declarator of a variable declaration, or ""
// for other statements.
func (w *walker) declName(n parse.Node) string {
	n = unwrap(n)
	switch n.Kind() {
	case "lexical_declaration", "variable_declaration":
		if decls := extract.NamedChildren(n, "variable_declarator"); len(decls) > 0 {
			n = decls[0]
		}
	}
	return w.name(n)
}

// links records the imports and re-exports among the top-level
// statements of a file.
func (w *walker) links(root parse.Node) {
	r := &refWalker{src: w.src, res: w.res}
	for i := uint(0); i < root.NamedChildCount(); i++ {
		switch n := root.NamedChild(i); n.Kind() {
		case "import_statement":
			r.importStatement(n)
		case "export_statement":
			r.reexport(n)
		}
	}
}

// supertypes records the types the extends clauses of the kept
// declarations name, as extend references enclosed by the declaration:
// what an inherited member is found through (deps spec section 5).
func (w *walker) supertypes(root parse.Node) {
	r := &refWalker{src: w.src, enc: extract.NewEncloser(w.res.Symbols), res: &extract.FileResult{},
		locals: extract.NewLocals()}
	var visit func(n parse.Node)
	visit = func(n parse.Node) {
		if k := n.Kind(); k == "extends_clause" || k == "extends_type_clause" {
			r.heritage(extract.RefExtend, n)
			return
		}
		for i := uint(0); i < n.NamedChildCount(); i++ {
			visit(n.NamedChild(i))
		}
	}
	visit(root)
	for _, ref := range r.res.References {
		if ref.Kind == extract.RefExtend && ref.Enclosing != "" {
			w.res.References = append(w.res.References, ref)
		}
	}
}

// apiSymbols keeps the public symbols and the protected members of
// public containers, in order. A member follows its container, and a
// member of a public container is package visible only when protected.
func apiSymbols(syms []extract.Symbol) []extract.Symbol {
	public := map[string]bool{}
	kept := syms[:0]
	for _, s := range syms {
		switch {
		case s.Visibility == extract.Public:
			public[s.Qualified] = true
		case s.Visibility != extract.Package || !public[s.Container]:
			continue
		}
		kept = append(kept, s)
	}
	return kept
}

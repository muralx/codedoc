package resolve

import (
	"slices"

	"github.com/muralx/codedoc/internal/store"
)

// bare resolves a name without qualifier through the scoping of its
// language only; the unique tier never applies to it (spec 7.3). The
// same file comes first; then Go looks in the same package and in the
// packages its file dot-imports; Python in the modules its file
// star-imports; Java walks the members the enclosing class inherits
// through resolved extends edges and, for names other than calls, the
// same package and the wildcard-imported packages. A bare name none of
// these declares is unresolved: a global, a builtin, or a name of
// another package that Go would have qualified.
func (r *refResolver) bare(ref store.Reference, c candidates) outcome {
	if out, ok := decides(c.inFile(ref.FileID)); ok {
		return out
	}
	switch c.fam {
	case "go":
		if out, ok := decides(c.inModules(map[int64]bool{ref.ModuleID: true})); ok {
			return out
		}
		return r.viaUnqualifiedImports(ref, c, func(imp store.Import) bool { return imp.Alias == "." })
	case "py":
		return r.viaUnqualifiedImports(ref, c, func(imp store.Import) bool {
			return slices.Equal(imp.Names, []string{"*"})
		})
	case "java":
		return r.javaBare(ref, c)
	}
	return outcome{}
}

// javaBare resolves a bare Java name outside its file: inherited members
// first, then, except for calls (a bare call names a method of the class
// or its supertypes), the package and wildcard imports.
func (r *refResolver) javaBare(ref store.Reference, c candidates) outcome {
	if out, ok := r.inherited(ref, c); ok {
		return out
	}
	if ref.Kind == "call" {
		return outcome{}
	}
	if out, ok := decides(c.inModules(r.scopeOf(ref.ModuleID, "java"))); ok {
		return out
	}
	return r.viaWildcard(ref, c)
}

// viaUnqualifiedImports resolves a bare name through the imports of its
// file that bring names in unqualified (Go dot imports, Python star
// imports): the candidates in the union of the imported modules and
// their descendants, at import confidence.
func (r *refResolver) viaUnqualifiedImports(ref store.Reference, c candidates, brings func(store.Import) bool) outcome {
	in := map[int64]bool{}
	for _, imp := range r.imports[ref.FileID] {
		if brings(imp) && imp.ResolvedModuleID != 0 {
			for id := range r.scopeOf(imp.ResolvedModuleID, c.fam) {
				in[id] = true
			}
		}
	}
	return pick(c.inModules(in), ConfidenceImport)
}

// maxInheritance bounds the extends chain walked for one reference.
const maxInheritance = 16

// inherited resolves a bare Java name to a member of a supertype of the
// enclosing class, walking resolved extends edges nearest first: local
// when the supertype is in the reference's package, else import. ok is
// false when no supertype declares the name.
func (r *refResolver) inherited(ref store.Reference, c candidates) (outcome, bool) {
	class := enclosingClass(ref)
	if class == "" {
		return outcome{}, false
	}
	types, err := r.members(class, map[int64]bool{ref.ModuleID: true})
	if err != nil || len(types) != 1 {
		return outcome{}, false
	}
	seen := map[int64]bool{types[0].ID: true}
	for level, id := 0, types[0].ID; level < maxInheritance; level++ {
		supers, err := r.b.Supertypes(id)
		if err != nil || len(supers) == 0 || seen[supers[0].ID] {
			return outcome{}, false
		}
		sup := supers[0]
		seen[sup.ID] = true
		own := c.member(sup.Qualified+"."+ref.Name, map[int64]bool{sup.ModuleID: true})
		if len(own) > 0 {
			conf := ConfidenceImport
			if r.scopeOf(ref.ModuleID, "java")[sup.ModuleID] {
				conf = ConfidenceLocal
			}
			return pick(own, conf), true
		}
		id = sup.ID
	}
	return outcome{}, false
}

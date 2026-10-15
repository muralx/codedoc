package resolve

import (
	"strings"

	"github.com/muralx/codedoc/internal/store"
)

// viaDeps resolves a reference the workspace tiers left unresolved to a
// symbol of a third-party package (deps spec section 6): a name bound
// by an import whose specifier belongs to a dependency of the file's
// nearest manifest, or a member of a dependency type reached through a
// field of the enclosing type or through a local variable of that type
// (see localRoot). The edge carries the stable key of the
// dependency symbol, that of the declaring type for an inherited
// member, at import confidence.
func (r *refResolver) viaDeps(ref store.Reference) outcome {
	fam := r.idx.family(ref.Lang)
	if ecosystems[fam] == "" || !r.deps.ready() {
		return outcome{}
	}
	sy, ok := r.depTarget(ref, fam)
	if !ok {
		return outcome{}
	}
	return outcome{confidence: ConfidenceImport, depKey: sy.StableKey}
}

// depTarget finds the dependency symbol a reference names.
func (r *refResolver) depTarget(ref store.Reference, fam string) (store.Symbol, bool) {
	if bind := r.importFor(ref, fam); bind.ok && ref.RecvType == "" {
		if bind.module != 0 {
			return store.Symbol{}, false
		}
		bd, ok := r.deps.bind(bind.imp, fam)
		if !ok {
			return store.Symbol{}, false
		}
		return r.deps.imported(bd, bind.imp, ref, fam)
	}
	typ, ok := r.depReceiver(ref, fam)
	if !ok {
		return store.Symbol{}, false
	}
	bd, ok := r.deps.bind(*typ.imp, fam)
	if !ok {
		return store.Symbol{}, false
	}
	container, ok := r.deps.top(bd, typ.qualified, nil, fam == "go")
	if !ok {
		return store.Symbol{}, false
	}
	return r.deps.member(bd.pkg, container, ref.Name, kindsFor(ref, fam, false))
}

// imported finds what a reference bound by an import of a package
// names: the top-level declaration with its name (Server, pkg.Name,
// ns.Name), or through a named TypeScript import the member of that
// declaration, inherited ones included (Server.create). A TypeScript
// name the entry file does not declare is looked for in the files it
// re-exports, and a member of a namespace the package exports (import {
// z }; z.object()) in the files of that namespace (see
// depSource.exported). A longer qualifier goes through a value whose
// type is unknown and names nothing.
func (d *depSource) imported(bd depBind, imp store.Import, ref store.Reference, fam string) (store.Symbol, bool) {
	kinds := kindsFor(ref, fam, true)
	head, rest, _ := strings.Cut(ref.Qualifier, ".")
	orig, named := importedAs(imp, head)
	switch {
	case ref.Qualifier == "":
		return d.bareImport(bd, imp, ref.Name, kinds, fam)
	case rest != "" || strings.HasSuffix(head, "()"):
		return store.Symbol{}, false
	case fam == "ts" && named:
		if container, ok := d.top(bd, orig, nil, false); ok {
			return d.member(bd.pkg, container, ref.Name, kinds)
		}
		return d.exported(bd, orig, ref.Name, kinds)
	case fam == "ts":
		if sy, ok := d.exported(bd, "", ref.Name, kinds); ok {
			return sy, true
		}
	}
	return d.top(bd, ref.Name, kinds, fam == "go")
}

// bareImport finds the declaration a bare name bound by an import
// names: the one of the module the specifier names, else in TypeScript
// the one its entry file re-exports, else the only one of the package.
func (d *depSource) bareImport(bd depBind, imp store.Import, name string, kinds []string, fam string) (store.Symbol, bool) {
	if fam != "ts" {
		return d.top(bd, name, kinds, false)
	}
	if orig, ok := importedAs(imp, name); ok {
		name = orig
	}
	if sy, ok := d.exported(bd, "", name, kinds); ok {
		return sy, true
	}
	return d.top(bd, name, kinds, false)
}

// depReceiver follows the receiver chain of a qualified reference (see
// receiverRoot) to a type declared in a third-party package: the type of
// the last field of the chain, which may itself be a field of a
// dependency type, or of the local variable it starts with. ok is false
// when the chain ends anywhere else.
func (r *refResolver) depReceiver(ref store.Reference, fam string) (recvType, bool) {
	if ref.Qualifier == "" {
		return recvType{}, false
	}
	typ, fields, ok := r.receiverRoot(ref, fam)
	if !ok || len(fields) == 0 && !typ.external {
		return recvType{}, false
	}
	for _, field := range fields {
		if typ, ok = r.fieldType(typ, field, fam); !ok {
			return recvType{}, false
		}
	}
	return typ, typ.external && typ.imp != nil
}

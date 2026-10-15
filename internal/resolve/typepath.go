package resolve

import (
	"slices"
	"strings"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/store"
)

// typed is what a step of a receiver chain lands on: a type already
// looked up (named), or a type as a declaration writes it (text), which
// may be an array or a promise and is looked up, from the file of the
// declaring symbol at, only when a member of it is asked for.
type typed struct {
	named   recvType
	isNamed bool
	text    string
	at      store.Target
	// far marks a declaration reached through an import: what it leads
	// to is at import confidence.
	far bool
	// imp is set for a declaration of a third-party package: the import
	// the package was reached through.
	imp *store.Import
}

// known wraps a type already looked up.
func known(typ recvType) typed { return typed{named: typ, isNamed: true} }

// outside is a type no symbol of the workspace or of an indexed
// dependency declares.
var outside = known(recvType{external: true})

// follow takes the steps of a type path from v (see
// extract.ParseTypePath) and looks up the type they end on. ok is false
// when a step cannot be taken from declared types: an unannotated
// field, a union, a type parameter, a member the type does not declare.
func (r *refResolver) follow(v typed, steps []extract.TypeStep, fam string) (recvType, bool) {
	for _, st := range steps {
		var ok bool
		if v, ok = r.step(v, st, fam); !ok {
			return recvType{}, false
		}
	}
	return r.settle(v, fam)
}

// step takes one step from v: to an element or an awaited value of a
// declared type, or to the declared type of a member of the type v
// names. What lies outside the workspace stays there.
func (r *refResolver) step(v typed, st extract.TypeStep, fam string) (typed, bool) {
	if v.isNamed && v.named.external && v.named.imp == nil {
		return v, true
	}
	switch st.Kind {
	case extract.StepElem:
		v.text = elemText(fam, v.text)
		return v, !v.isNamed && v.text != ""
	case extract.StepAwait:
		if !v.isNamed {
			v.text = awaitedText(v.text)
		}
		return v, true
	}
	typ, ok := r.settle(v, fam)
	if !ok {
		return typed{}, false
	}
	return r.memberType(typ, st, fam)
}

// settle looks up the type v stands for: a plain named type, resolved
// as the file of its declaration reads it. ok is false for any other
// type (see typeNameOf). A type a dependency declares is one of the
// same package, or lies outside what is indexed.
func (r *refResolver) settle(v typed, fam string) (recvType, bool) {
	if v.isNamed {
		return v.named, true
	}
	name := typeNameOf(fam, v.text)
	switch {
	case v.imp != nil:
		return depNamed(name, v.imp, fam), true
	case name == "":
		return recvType{}, false
	}
	var typ recvType
	var ok bool
	switch fam {
	case "go":
		typ, ok = r.goNamed(v.at, name)
	case "ts":
		typ, ok = r.tsNamed(siteOf(v.at), name, 0)
	case "java":
		typ, ok = r.javaType(v.at, name)
	}
	if ok && v.far {
		typ.conf = ConfidenceImport
	}
	return typ, ok
}

// depNamed is the type a declaration of a third-party package names:
// one of the package the import imp leads to, or outside what is
// indexed when it is no plain name of that package.
func depNamed(name string, imp *store.Import, fam string) recvType {
	if name == "" || strings.Contains(name, ".") || fam == "go" && goPredeclared[name] {
		return recvType{external: true}
	}
	return recvType{qualified: name, external: true, imp: imp}
}

// siteOf is the place a TypeScript declaration reads type names from,
// as tsNamed takes it: its file, its module and the file prefix of its
// qualified name.
func siteOf(at store.Target) store.Reference {
	return store.Reference{FileID: at.FileID, ModuleID: at.ModuleID, Enclosing: at.Qualified}
}

// memberType returns the declared type of the member a step names in
// typ: of a field, or the result of a method (see declaredText). In Go
// the member may be promoted from an embedded struct. ok is false when
// typ declares no single such member, it declares no type, or the type
// is a type parameter of the member or of typ.
func (r *refResolver) memberType(typ recvType, st extract.TypeStep, fam string) (typed, bool) {
	if typ.suffix {
		var ok bool
		if typ, ok = r.pin(typ); !ok {
			return typed{}, false
		}
	}
	if typ.external {
		return r.depMemberType(typ, st, fam)
	}
	ms, err := r.members(typ.qualified+"."+st.Name, typ.scope)
	if err == nil && len(ms) == 0 && fam == "go" {
		ms = r.goPromoted(typ, st.Name)
	}
	if err != nil || len(ms) != 1 {
		return typed{}, false
	}
	m := ms[0]
	text := declaredText(fam, st.Kind, m.Kind, m.Signature)
	if text == "" || r.typeParameter(m, text, fam) {
		return typed{}, false
	}
	return typed{text: text, at: m, far: typ.conf == ConfidenceImport || !typ.scope[m.ModuleID]}, true
}

// depMemberType is memberType for a type of a third-party package: the
// declared type of its member, read from deps.db. A type that is not
// indexed leads outside.
func (r *refResolver) depMemberType(typ recvType, st extract.TypeStep, fam string) (typed, bool) {
	if typ.imp == nil || !r.deps.ready() {
		return outside, true
	}
	bd, ok := r.deps.bind(*typ.imp, fam)
	if !ok {
		return outside, true
	}
	container, ok := r.deps.top(bd, typ.qualified, nil, fam == "go")
	if !ok {
		return outside, true
	}
	m, ok := r.deps.member(bd.pkg, container, st.Name, []string{"field", "method"})
	if !ok {
		return typed{}, false
	}
	text := declaredText(fam, st.Kind, m.Kind, m.Signature)
	return typed{text: text, imp: typ.imp}, text != ""
}

// typeParameter reports whether the type a member declares is a type
// parameter of the member itself or of the type that holds it.
func (r *refResolver) typeParameter(m store.Target, text, fam string) bool {
	name := typeNameOf(fam, text)
	if name == "" {
		return false
	}
	if declaresParam(fam, strings.TrimSpace(m.Signature), name, true) {
		return true
	}
	dot := strings.LastIndex(m.Qualified, ".")
	if dot < 0 {
		return false
	}
	holders, err := r.members(m.Qualified[:dot], map[int64]bool{m.ModuleID: true})
	if err != nil {
		return false
	}
	for _, h := range holders {
		if declaresParam(fam, strings.TrimSpace(h.Signature), name, false) {
			return true
		}
	}
	return false
}

// pin replaces a TypeScript type known by its name in the modules an
// import leads to (recvType.suffix) with the one declaration of that
// name there, through the aliases of plain named types. ok is false
// when no single declaration is found.
func (r *refResolver) pin(typ recvType) (recvType, bool) {
	for depth := 0; typ.suffix && depth < maxAliases; depth++ {
		decls, err := r.namedIn(typ.qualified, typ.scope)
		decls = filter(decls, func(t store.Target) bool {
			return (t.Qualified == typ.qualified || strings.HasSuffix(t.Qualified, "/"+typ.qualified)) &&
				slices.Contains(typeKinds, t.Kind)
		})
		if err != nil || len(decls) != 1 {
			return typ, false
		}
		d := decls[0]
		if m := tsAlias.FindStringSubmatch(d.Signature); m != nil && d.Kind == "type" {
			var ok bool
			if typ, ok = r.tsNamed(siteOf(d), m[1], depth+1); !ok {
				return typ, false
			}
			typ.conf = ConfidenceImport
			continue
		}
		typ = recvType{qualified: d.Qualified, scope: map[int64]bool{d.ModuleID: true}, conf: typ.conf}
	}
	return typ, !typ.suffix
}

// maxEmbedding bounds the depth of embedded structs searched for a
// promoted member.
const maxEmbedding = 3

// goPromoted returns the member name that a Go struct gets from the
// structs it embeds, as the language promotes it: the one member of
// that name at the shallowest depth that has any. It returns nil when
// that depth has several, or when a shallower or equal depth embeds a
// type the index cannot see into (a type of another module that is not
// indexed, an interface), which may declare the name too.
func (r *refResolver) goPromoted(typ recvType, name string) []store.Target {
	level, seen := []recvType{typ}, map[string]bool{scopeName(typ): true}
	for depth := 0; depth < maxEmbedding && len(level) > 0; depth++ {
		var next []recvType
		var found []store.Target
		opaque := false
		for _, t := range level {
			embedded, all := r.goEmbedded(t)
			opaque = opaque || !all
			for _, e := range embedded {
				if key := scopeName(e); !seen[key] {
					seen[key] = true
					next = append(next, e)
					ms, _ := r.members(e.qualified+"."+name, e.scope)
					found = append(found, ms...)
				}
			}
		}
		switch {
		case len(found) == 1:
			return found
		case len(found) > 1 || opaque:
			return nil
		}
		level = next
	}
	return nil
}

// goEmbedded returns the types of the workspace a Go struct embeds; all
// is false when it embeds a type that is none, or one that is no
// struct, whose own embedded types the index does not hold (see
// goPromoted).
func (r *refResolver) goEmbedded(typ recvType) (embedded []recvType, all bool) {
	fields, err := r.fields(typ)
	if err != nil {
		return nil, false
	}
	all = true
	for _, f := range fields {
		sig := strings.TrimSpace(f.Signature)
		m := goTypeName.FindStringSubmatch(sig)
		if m == nil || m[1][strings.LastIndex(m[1], ".")+1:] != f.Name {
			continue
		}
		e, ok := r.settle(typed{text: sig, at: f, far: typ.conf == ConfidenceImport}, "go")
		if !ok || e.external {
			all = false
			continue
		}
		if decl, err := r.members(e.qualified, e.scope); err != nil || len(decl) != 1 || decl[0].Kind != "struct" {
			all = false
		}
		embedded = append(embedded, e)
	}
	return embedded, all
}

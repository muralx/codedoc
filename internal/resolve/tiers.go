package resolve

import (
	"path"
	"regexp"
	"strings"

	"github.com/muralx/codedoc/internal/store"
)

// outcome is the resolution of one reference: a confidence with one
// target (a symbol of the workspace, or of a dependency by its key), or
// ConfidenceAmbiguous with several; no confidence means unresolved, with
// count candidates when there were too many.
type outcome struct {
	confidence string
	dst        []int64
	count      int
	// depKey is set instead of dst when the reference names a symbol of
	// a third-party package: its stable key in deps.db.
	depKey string
}

// Symbol kinds a reference kind may name.
var (
	typeKinds   = []string{"struct", "class", "interface", "type", "enum", "trait"}
	callKinds   = []string{"function", "method"}
	fieldKinds  = []string{"field", "method"}
	importField = []string{"field", "method", "var", "const", "function"}
	superKinds  = []string{"class", "interface"}
)

// resolve applies the tiers of spec 7.3 to one reference. A qualifier
// (or, in TS, Python and Java, a bare name) bound by an import restricts
// the candidates to the imported module and its descendants (in Java, the
// package's directories): the import tier. Otherwise a qualified
// reference goes through its receiver (see viaReceiver; a local variable
// with a known type hides an import of the same name) and else falls
// to the unique tier, matched on name and arity across the workspace
// (through a variable whose type is unknown the one match is a guess,
// kept as an ambiguous edge with that candidate: see blind); a bare
// name resolves only through language scoping (see bare). What all
// of them leave unresolved is looked up in the third-party packages the
// workspace depends on (see viaDeps).
func (r *refResolver) resolve(ref store.Reference) outcome {
	if _, ok := edgeKinds[ref.Kind]; !ok {
		return outcome{}
	}
	out, _ := r.resolveKinds(ref, func(fam string, imported bool) []string { return kindsFor(ref, fam, imported) })
	if out.confidence == "" && out.count == 0 {
		return r.viaDeps(ref)
	}
	return out
}

// resolveKinds applies the tiers to a reference naming a symbol of the
// kinds kinds returns for its family and import binding, and returns the
// candidates it chose among.
func (r *refResolver) resolveKinds(ref store.Reference, kinds func(fam string, imported bool) []string) (outcome,
	candidates) {
	fam := r.idx.family(ref.Lang)
	bind := r.importFor(ref, fam)
	if ref.RecvType != "" {
		bind = binding{} // a variable hides the import of its name
	}
	name := ref.Name
	if bind.orig != "" {
		name = bind.orig
	}
	c := candidates{x: r.idx, name: name, fam: fam, kinds: kinds(fam, bind.ok)}
	switch {
	case !c.any():
		return outcome{}, c
	case bind.ok:
		return r.viaImport(ref, c, bind.module), c
	case ref.Qualifier == "":
		return r.bare(ref, c), c
	}
	if out, ok := r.viaReceiver(ref, c); ok {
		return out, c
	}
	out := r.unique(ref, c)
	if out.confidence == ConfidenceUnique && r.blind(ref, fam) {
		out.confidence = ConfidenceAmbiguous
	}
	return out, c
}

// Kinds a Go reference qualified by a package name alone (pkg.Name) may
// name: package-level declarations, never methods or fields.
var goPackageKinds = append([]string{"function", "var", "const"}, typeKinds...)

// javaFieldKinds are the kinds a Java field reference may name.
var javaFieldKinds = []string{"field", "const"}

// kindsFor returns the symbol kinds a reference can name. A call through
// a receiver (a qualifier that is not an import) names methods only; in
// Go, pkg.Name names a package-level declaration; a Java field reference
// names a field or a constant (static final field, enum constant).
func kindsFor(ref store.Reference, fam string, imported bool) []string {
	switch {
	case goPackageRef(ref, fam, imported):
		return goPackageKinds
	case ref.Kind == "field":
		return fieldKindsFor(fam, imported)
	case ref.Kind == "call" && ref.Qualifier != "" && !imported:
		return []string{"method"}
	case ref.Kind == "call":
		return callKinds
	case ref.Kind == "type" || ref.Kind == "instantiate":
		return typeKinds
	}
	return superKinds
}

// goPackageRef reports whether a Go call or field reference is
// qualified by an imported package name alone (pkg.Name).
func goPackageRef(ref store.Reference, fam string, imported bool) bool {
	return (ref.Kind == "call" || ref.Kind == "field") && imported && fam == "go" && !strings.Contains(ref.Qualifier, ".")
}

// fieldKindsFor returns the kinds a field reference may name.
func fieldKindsFor(fam string, imported bool) []string {
	switch {
	case fam == "java":
		return javaFieldKinds
	case imported:
		return importField
	}
	return fieldKinds
}

// viaImport keeps the candidates in the imported module or below it (in
// Java, in any directory of the imported package, and through a class
// qualifier only the members of that class: Status.ACTIVE); an import
// that resolved to no workspace module names nothing indexed.
func (r *refResolver) viaImport(ref store.Reference, c candidates, mod int64) outcome {
	if mod == 0 {
		return outcome{}
	}
	in := r.scopeOf(mod, c.fam)
	if c.fam == "java" && ref.Qualifier != "" {
		return pick(c.memberOf(ref.Qualifier, in), ConfidenceImport)
	}
	return pick(c.inModules(in), ConfidenceImport)
}

// decides picks the local candidates of a bare name in one scope; ok is
// false when the scope has none.
func decides(same []store.Target) (outcome, bool) {
	if len(same) == 0 {
		return outcome{}, false
	}
	return pick(same, ConfidenceLocal), true
}

// unique keeps the candidates whose parameter count admits the call's
// argument count: one is the unique tier, several are ambiguous. The
// outcome depends only on name, family, kinds and arity, so it is
// computed once per run for each.
func (r *refResolver) unique(ref store.Reference, c candidates) outcome {
	k := uniqueKey{c.name, c.fam, strings.Join(c.kinds, ","), ref.Arity}
	if out, ok := r.uniques[k]; ok {
		return out
	}
	out := pick(filter(c.all(), func(t store.Target) bool { return admits(t, ref.Arity) }), ConfidenceUnique)
	r.uniques[k] = out
	return out
}

type uniqueKey struct {
	name, fam, kinds string
	arity            int
}

// pick turns a candidate set into an outcome: one target at tier, several
// ambiguous (up to MaxCandidates), none unresolved.
func pick(cands []store.Target, tier string) outcome {
	switch {
	case len(cands) == 0:
		return outcome{}
	case len(cands) == 1:
		return outcome{confidence: tier, dst: []int64{cands[0].ID}}
	case len(cands) > MaxCandidates:
		return outcome{count: len(cands)}
	}
	ids := make([]int64, len(cands))
	for i, t := range cands {
		ids[i] = t.ID
	}
	return outcome{confidence: ConfidenceAmbiguous, dst: ids}
}

func filter(ts []store.Target, keep func(store.Target) bool) []store.Target {
	var out []store.Target
	for _, t := range ts {
		if keep(t) {
			out = append(out, t)
		}
	}
	return out
}

// binding is the import that binds a reference: its module, and for a
// bare name bound through an alias (from m import X as Y) the original
// name.
type binding struct {
	module int64
	orig   string
	ok     bool
	// imp is the binding import of a Go or TS reference.
	imp store.Import
}

// importFor returns the import that binds the reference: in Go the
// import whose package name is the qualifier's first segment; in TS the
// import whose default or namespace alias, or one of whose named
// imports, is that segment, or for a bare name the name itself; Python
// and Java as pyBinds and javaBinds say.
func (r *refResolver) importFor(ref store.Reference, fam string) binding {
	head, _, _ := strings.Cut(ref.Qualifier, ".")
	head = strings.TrimSuffix(head, "()")
	for _, imp := range r.imports[ref.FileID] {
		switch fam {
		case "py":
			if b := r.pyBinds(imp, head, ref); b.ok {
				return b
			}
		case "java":
			if javaBinds(imp, head, ref) {
				return binding{module: imp.ResolvedModuleID, ok: true}
			}
		default:
			if orig, ok := binds(imp, fam, head, ref); ok {
				return binding{module: imp.ResolvedModuleID, ok: true, imp: imp, orig: orig}
			}
		}
	}
	return binding{}
}

// binds reports whether a Go or TS import binds the qualifier head, or
// for a bare TS reference the name itself; orig is the name the source
// exports when a bare reference names it through an alias (import { a
// as b }).
func binds(imp store.Import, fam, head string, ref store.Reference) (orig string, ok bool) {
	switch {
	case fam == "go":
		return "", head != "" && head == goPackageName(imp)
	case fam != "ts":
		return "", false
	case ref.Qualifier == "":
		if imp.Alias == ref.Name {
			return "", true
		}
		if orig, ok = importedAs(imp, ref.Name); orig == ref.Name {
			orig = ""
		}
		return orig, ok
	}
	_, named := importedAs(imp, head)
	return "", head != "" && (imp.Alias == head || named)
}

// majorVersion matches a Go major version path suffix (v2, v3, ...).
var majorVersion = regexp.MustCompile(`^v[0-9]+$`)

// goPackageName is the name a Go import binds: its alias, else the last
// path segment, skipping a major version segment and dropping a .vN
// suffix (gopkg.in/yaml.v3) and a go- prefix. Blank and dot imports bind
// nothing.
func goPackageName(imp store.Import) string {
	if imp.Alias != "" {
		if imp.Alias == "_" || imp.Alias == "." {
			return ""
		}
		return imp.Alias
	}
	p := imp.Raw
	base := path.Base(p)
	if majorVersion.MatchString(base) && path.Dir(p) != "." {
		base = path.Base(path.Dir(p))
	}
	if i := strings.Index(base, ".v"); i > 0 {
		base = base[:i]
	}
	return strings.TrimPrefix(base, "go-")
}

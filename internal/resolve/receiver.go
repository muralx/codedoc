package resolve

import (
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/store"
)

// recvType is the type a receiver chain lands on: its qualified name, the
// modules that may declare its members, and the confidence of the chain.
// external marks a type declared outside the workspace.
type recvType struct {
	qualified string
	scope     map[int64]bool
	conf      string
	external  bool
	// imp is the import an external type is reached through, when known:
	// the dependency tier looks the type up in the package it names.
	imp *store.Import
	// suffix marks a qualified name that lacks the file prefix of a
	// TypeScript symbol: members match by the end of their name.
	suffix bool
}

// members returns the candidates that are the member name of the type.
func (t recvType) members(c candidates, name string) []store.Target {
	if t.suffix {
		return c.memberOf(t.qualified, t.scope)
	}
	return c.member(t.qualified+"."+name, t.scope)
}

// selfQualifiers are, per language family, the receivers that name the
// enclosing class.
var selfQualifiers = map[string]map[string]bool{
	"ts": {"this": true}, "py": {"self": true, "cls": true}, "java": {"this": true},
}

// viaReceiver resolves a qualified reference through what its receiver
// names: this, self or cls (the enclosing class), the receiver of the
// enclosing Go method (func (svc *Service) ...: svc), or in Java a field
// of the enclosing class; then through each further qualifier segment as
// a field whose declared type the field's signature gives (Go, TS and
// Java: see fieldType). The receiver may also be a local variable or
// parameter whose declaration names its type or leads to it (see
// localRoot).
// The reference resolves to the member of that type with its name (in
// Go also one promoted from an embedded struct), at local confidence,
// or import when a type was found through an import; a type declared
// outside the workspace leaves it unresolved. ok is false when the
// chain cannot be followed or the type declares no such member: the
// unique tier decides then.
func (r *refResolver) viaReceiver(ref store.Reference, c candidates) (outcome, bool) {
	fam := c.fam
	typ, path, ok := r.receiverRoot(ref, fam)
	if !ok {
		return outcome{}, false
	}
	if typ.external {
		return outcome{}, true
	}
	for i, field := range path {
		next, ok := r.fieldType(typ, field, fam)
		if !ok && i == 0 && fam == "java" && !selfQualifiers[fam][strings.Split(ref.Qualifier, ".")[0]] {
			next, ok = r.javaClassQualifier(ref)
			path = nil
		}
		if !ok {
			return outcome{}, false
		}
		if typ = next; typ.external {
			return outcome{}, true
		}
		if path == nil {
			break
		}
	}
	return r.ownMember(typ, c, ref.Name)
}

// ownMember picks the member name of typ among the candidates: one it
// declares, else in Go one promoted from a struct it embeds (at import
// confidence when that struct lies in another package). ok is false
// when typ has no such member.
func (r *refResolver) ownMember(typ recvType, c candidates, name string) (outcome, bool) {
	own := typ.members(c, name)
	if len(own) == 0 && c.fam == "go" {
		if own = c.keep(r.goPromoted(typ, name)); len(own) == 1 && !typ.scope[own[0].ModuleID] {
			typ.conf = ConfidenceImport
		}
	}
	if len(own) == 0 {
		return outcome{}, false
	}
	return pick(own, typ.conf), true
}

// receiverRoot returns the type the qualifier's first segment names and
// the segments left to follow as fields. A variable the extractor saw
// declared decides alone (see localRoot): it hides the receiver of the
// enclosing method and the package variables of its name.
func (r *refResolver) receiverRoot(ref store.Reference, fam string) (recvType, []string, bool) {
	if ref.RecvType != "" {
		return r.localRoot(ref, fam)
	}
	segs := strings.Split(ref.Qualifier, ".")
	here := map[int64]bool{ref.ModuleID: true}
	if fam == "go" {
		typ, ok := r.goOuter(ref, segs[0])
		return typ, segs[1:], ok
	}
	class := enclosingClass(ref)
	if class == "" {
		return recvType{}, nil, false
	}
	root := recvType{qualified: class, scope: here, conf: ConfidenceLocal}
	switch {
	case selfQualifiers[fam][segs[0]]:
		return root, segs[1:], true
	case fam == "java":
		return root, segs, true
	}
	return recvType{}, nil, false
}

// goMethodReceiver matches the receiver of a Go method signature: its
// name and the base name of its type.
var goMethodReceiver = regexp.MustCompile(`^func\s*\(\s*([A-Za-z_]\w*)\s+\*?\s*([A-Za-z_]\w*)`)

// goReceiver returns the receiver name and type of a Go method signature,
// empty for a function or an unnamed receiver.
func goReceiver(sig string) (name, typ string) {
	m := goMethodReceiver.FindStringSubmatch(sig)
	if m == nil || m[1] == "_" {
		return "", ""
	}
	return m[1], m[2]
}

// classKinds are the symbol kinds that hold members.
var classKinds = []string{"class", "interface", "enum", "struct", "trait"}

// enclosingClass returns the qualified name of the class a reference
// sits in: the enclosing symbol when it is a class, else its container
// (the qualified name up to its last dot, after any TS file prefix).
func enclosingClass(ref store.Reference) string {
	if slices.Contains(classKinds, ref.EnclosingKind) {
		return ref.Enclosing
	}
	dot := strings.LastIndex(ref.Enclosing, ".")
	if dot <= strings.LastIndex(ref.Enclosing, "/")+1 {
		return ""
	}
	return ref.Enclosing[:dot]
}

// fieldType returns the declared type of field of typ, where the
// field's signature names a plain type: a struct field (Go; also one
// promoted from an embedded struct), a class field, an interface
// property or a property of an object type alias (TS; its annotation,
// the class its initializer constructs, or the result of a get
// accessor), a Java field. The type is looked up from the file that
// declares the field. A qualifier segment that is a call (x.get().y:
// "get()") stands for the declared result of that method (Go, TS). ok
// is false for a member without such a type.
func (r *refResolver) fieldType(typ recvType, field, fam string) (recvType, bool) {
	st := extract.TypeStep{Kind: extract.StepField, Name: field}
	if method, isCall := strings.CutSuffix(field, "()"); isCall {
		st = extract.TypeStep{Kind: extract.StepResult, Name: method}
	}
	v, ok := r.memberType(typ, st, fam)
	if !ok {
		return recvType{}, false
	}
	return r.settle(v, fam)
}

// Field declarations: the names before a Go field's type, a Go type name
// (pkg.Name, *Name, Name[T]), a Java type name.
var (
	goFieldNames = regexp.MustCompile(`^[A-Za-z_]\w*(?:\s*,\s*[A-Za-z_]\w*)*\s+`)
	goTypeName   = regexp.MustCompile(`^\*?([A-Za-z_]\w*(?:\.[A-Za-z_]\w*)?)(?:\[.*\])?$`)
	javaTypeName = regexp.MustCompile(`^[A-Za-z_$][\w$]*$`)
)

// stripGenerics drops the type arguments (<...>) of a declaration.
func stripGenerics(s string) string {
	var b strings.Builder
	depth := 0
	for _, c := range s {
		switch {
		case c == '<':
			depth++
		case c == '>' && depth > 0:
			depth--
		case depth == 0:
			b.WriteRune(c)
		}
	}
	return b.String()
}

// goType resolves a Go field type: pkg.Name through the imports of the
// field's file, Name in the field's package.
func (r *refResolver) goType(f store.Target, expr string) (recvType, bool) {
	pkg, name, qualified := strings.Cut(expr, ".")
	if !qualified {
		return recvType{qualified: expr, scope: map[int64]bool{f.ModuleID: true}, conf: ConfidenceLocal}, true
	}
	imps, err := r.fileImports(f.FileID)
	if err != nil {
		return recvType{}, false
	}
	for _, imp := range imps {
		if goPackageName(imp) != pkg {
			continue
		}
		if imp.ResolvedModuleID == 0 {
			return recvType{qualified: name, external: true, imp: &imp}, true
		}
		return recvType{qualified: name, scope: map[int64]bool{imp.ResolvedModuleID: true}, conf: ConfidenceImport}, true
	}
	return recvType{}, false
}

// javaType resolves a Java field type: through a single-type import of
// the field's file, else in the field's package when it declares the
// type, else in the packages its file imports with a wildcard.
func (r *refResolver) javaType(f store.Target, expr string) (recvType, bool) {
	imps, err := r.fileImports(f.FileID)
	if err != nil {
		return recvType{}, false
	}
	var wild []int64
	for _, imp := range imps {
		switch {
		case imp.Alias == expr && imp.ResolvedModuleID == 0:
			return recvType{external: true}, true
		case imp.Alias == expr:
			return recvType{qualified: expr, scope: r.scopeOf(imp.ResolvedModuleID, "java"), conf: ConfidenceImport}, true
		case strings.HasSuffix(imp.Raw, ".*") && imp.ResolvedModuleID != 0:
			wild = append(wild, imp.ResolvedModuleID)
		}
	}
	pkg := r.scopeOf(f.ModuleID, "java")
	if types, err := r.members(expr, pkg); err == nil && len(types) > 0 {
		return recvType{qualified: expr, scope: pkg, conf: ConfidenceLocal}, true
	}
	scope := map[int64]bool{}
	for _, m := range wild {
		for id := range r.scopeOf(m, "java") {
			scope[id] = true
		}
	}
	return recvType{qualified: expr, scope: scope, conf: ConfidenceImport}, len(scope) > 0
}

// javaClassQualifier resolves a Java qualifier that names a class
// (Status in Status.ACTIVE, Outer.Inner in Outer.Inner.X) the way a
// field type resolves from the reference's file: single-type import,
// same package, wildcard imports.
func (r *refResolver) javaClassQualifier(ref store.Reference) (recvType, bool) {
	head, _, _ := strings.Cut(ref.Qualifier, ".")
	typ, ok := r.javaType(store.Target{FileID: ref.FileID, ModuleID: ref.ModuleID}, head)
	typ.qualified = ref.Qualifier
	return typ, ok
}

// members returns the symbols with a qualified name in the modules of
// scope, cached.
func (r *refResolver) members(qualified string, scope map[int64]bool) ([]store.Target, error) {
	return r.cached(scopeName(recvType{qualified: qualified, scope: scope}), func(ids []int64) ([]store.Target, error) {
		return r.b.TargetsByQualified(qualified, ids)
	}, scope)
}

// namedIn returns the symbols with a name in the modules of scope,
// cached.
func (r *refResolver) namedIn(name string, scope map[int64]bool) ([]store.Target, error) {
	return r.cached("name\x00"+scopeName(recvType{qualified: name, scope: scope}), func(ids []int64) ([]store.Target, error) {
		return r.b.TargetsNamedIn(name, ids)
	}, scope)
}

// fields returns the fields a type declares, cached.
func (r *refResolver) fields(typ recvType) ([]store.Target, error) {
	return r.cached("fields\x00"+scopeName(typ), func(ids []int64) ([]store.Target, error) {
		return r.b.TargetMembers(typ.qualified, "field", ids)
	}, typ.scope)
}

// cached returns what read gives for the modules of scope, read once
// per key.
func (r *refResolver) cached(key string, read func(ids []int64) ([]store.Target, error),
	scope map[int64]bool) ([]store.Target, error) {
	if ts, ok := r.memberCache[key]; ok {
		return ts, nil
	}
	ids := keys(scope)
	slices.Sort(ids)
	ts, err := read(ids)
	if err == nil {
		r.memberCache[key] = ts
	}
	return ts, err
}

// scopeName names a type by its qualified name and the modules it may
// lie in.
func scopeName(typ recvType) string {
	ids := keys(typ.scope)
	slices.Sort(ids)
	k := typ.qualified
	for _, id := range ids {
		k += "\x00" + strconv.FormatInt(id, 10)
	}
	return k
}

// fileImports returns the imports of a file, reading them when load did
// not.
func (r *refResolver) fileImports(fileID int64) ([]store.Import, error) {
	if imps, ok := r.imports[fileID]; ok || r.importsAll || r.importsRead[fileID] {
		return imps, nil
	}
	imps, err := r.b.Imports([]int64{fileID})
	if err != nil {
		return nil, err
	}
	r.imports[fileID], r.importsRead[fileID] = imps, true
	return imps, nil
}

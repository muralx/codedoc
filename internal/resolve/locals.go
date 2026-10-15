package resolve

import (
	"regexp"
	"strings"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/store"
)

// localRoot returns the type of the variable a qualifier starts with,
// where the extractor found its declaration to make it evident
// (store.Reference.RecvType: a typed declaration or parameter, a
// constructor call, a composite literal, the result of a function, or
// a type path from one of those through declared field types and
// method results: see extract.FieldOf), and the qualifier segments left
// to follow as fields. The root of the type is looked up from the
// reference's file as a field type is: through its imports, else in
// its package (Go) or file (TS); each step from the file that declares
// the member it reads (see follow). A builtin type, and a type that is
// neither, is external: no symbol of the workspace declares its
// members. ok is false when no type is recorded or it cannot be told: a
// variable declared without an evident type (extract.UnknownType), the
// result of a callee the index does not know, a step through a member
// without a plain declared type.
func (r *refResolver) localRoot(ref store.Reference, fam string) (recvType, []string, bool) {
	if ref.RecvType == "" || ref.RecvType == extract.UnknownType {
		return recvType{}, nil, false
	}
	segs := strings.Split(ref.Qualifier, ".")
	path := segs[min(extract.LocalSegments(ref.Qualifier), len(segs)):]
	root, steps := extract.ParseTypePath(ref.RecvType)
	v, steps, ok := r.rootType(ref, fam, root, steps)
	if !ok {
		return recvType{}, path, false
	}
	typ, ok := r.follow(v, steps, fam)
	return typ, path, ok
}

// rootType looks up the root of a type path recorded on ref: a type
// name, the enclosing class (extract.SelfRoot), a call (see goCallRoot
// and tsResult) or a name its file does not declare (see goOuter). It
// returns the steps left to take, which a call through a method
// receiver adds to.
func (r *refResolver) rootType(ref store.Reference, fam, root string,
	steps []extract.TypeStep) (typed, []extract.TypeStep, bool) {
	var typ recvType
	var ok bool
	switch callee, isCall := strings.CutSuffix(root, "()"); {
	case root == extract.BuiltinType || strings.HasPrefix(root, extract.ArrayPrefix):
		return outside, steps, true
	case fam == "go":
		return r.goRoot(ref, root, steps)
	case fam == "ts" && root == extract.SelfRoot:
		typ = recvType{qualified: enclosingClass(ref), scope: map[int64]bool{ref.ModuleID: true}, conf: ConfidenceLocal}
		ok = typ.qualified != ""
	case fam == "ts" && isCall:
		v, ok := r.tsResult(ref, callee)
		return v, steps, ok
	case fam == "ts":
		typ, ok = r.tsNamed(ref, root, 0)
	}
	return known(typ), steps, ok
}

// goRoot is rootType for Go: a call (see goCallRoot), a name the file
// does not declare (see goOuter), else a type name.
func (r *refResolver) goRoot(ref store.Reference, root string, steps []extract.TypeStep) (typed, []extract.TypeStep, bool) {
	at := store.Target{FileID: ref.FileID, ModuleID: ref.ModuleID}
	if callee, isCall := strings.CutSuffix(root, "()"); isCall {
		return r.goCallRoot(ref, at, callee, steps)
	}
	if name, outer := strings.CutPrefix(root, extract.OuterPrefix); outer {
		typ, ok := r.goOuter(ref, name)
		return known(typ), steps, ok
	}
	typ, ok := r.goNamed(at, root)
	return known(typ), steps, ok
}

// goCallRoot is rootType for a Go call: the first result of the
// function f or pkg.F (see goResult); for x.M where x is the receiver
// of the enclosing method, or no package but a variable of the
// reference's package, the result of the method M of its type.
func (r *refResolver) goCallRoot(ref store.Reference, at store.Target, callee string,
	steps []extract.TypeStep) (typed, []extract.TypeStep, bool) {
	x, method, qualified := strings.Cut(callee, ".")
	if recv, _ := goReceiver(ref.EnclosingSig); !qualified || recv != x {
		if v, ok := r.goResult(at, callee); ok || !qualified {
			return v, steps, ok
		}
	}
	typ, ok := r.goOuter(ref, x)
	return known(typ), append([]extract.TypeStep{{Kind: extract.StepResult, Name: method}}, steps...), ok
}

// goOuter returns the type of a name that no scope of the reference's
// file declares: the receiver of the enclosing method (func (svc
// *Service) ...: svc), else a variable of the package (see
// goPackageVar).
func (r *refResolver) goOuter(ref store.Reference, name string) (recvType, bool) {
	if recv, typ := goReceiver(ref.EnclosingSig); recv != "" && recv == name {
		return recvType{qualified: typ, scope: map[int64]bool{ref.ModuleID: true}, conf: ConfidenceLocal}, true
	}
	return r.goPackageVar(ref, name)
}

// goPredeclared are Go's predeclared types: a value of one has no
// method the index knows, except error's, which no symbol declares.
var goPredeclared = map[string]bool{
	"bool": true, "byte": true, "complex64": true, "complex128": true, "error": true, "float32": true,
	"float64": true, "int": true, "int8": true, "int16": true, "int32": true, "int64": true, "rune": true,
	"string": true, "uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true, "uintptr": true,
	"any": true,
}

// goNamed resolves a Go type name written in the file of at (see
// goType); a predeclared type is external.
func (r *refResolver) goNamed(at store.Target, expr string) (recvType, bool) {
	if goPredeclared[expr] {
		return recvType{external: true}, true
	}
	return r.goType(at, expr)
}

// goResult returns the type of the first result of the function a call
// names (New, kit.New), as its signature writes it, read from the one
// function the index or deps.db holds under that name. A function of a
// package outside the workspace that deps.db does not hold returns an
// external type; ok is false when the callee is no known function (a
// method value, a function variable) or it has no result.
func (r *refResolver) goResult(at store.Target, callee string) (typed, bool) {
	pkg, name, qualified := strings.Cut(callee, ".")
	if !qualified {
		return r.goResultOf(callee, map[int64]bool{at.ModuleID: true}, false)
	}
	imps, err := r.fileImports(at.FileID)
	if err != nil {
		return typed{}, false
	}
	for _, imp := range imps {
		if goPackageName(imp) != pkg {
			continue
		}
		if imp.ResolvedModuleID != 0 {
			return r.goResultOf(name, map[int64]bool{imp.ResolvedModuleID: true}, true)
		}
		return r.goDepResult(imp, name), true
	}
	return typed{}, false
}

// goResultOf is goResult for a function of the workspace declared in
// scope; the type is at least as far away as the function (far).
func (r *refResolver) goResultOf(name string, scope map[int64]bool, far bool) (typed, bool) {
	fns, err := r.members(name, scope)
	if err != nil || len(fns) != 1 || fns[0].Kind != "function" {
		return typed{}, false
	}
	text := goResultText(fns[0].Signature)
	return typed{text: text, at: fns[0], far: far}, text != ""
}

// goDepResult is goResult for a function of a package outside the
// workspace: the type its first result names in that package, or a
// type outside what is indexed when deps.db cannot tell.
func (r *refResolver) goDepResult(imp store.Import, name string) typed {
	if !r.deps.ready() {
		return outside
	}
	bd, ok := r.deps.bind(imp, "go")
	if !ok {
		return outside
	}
	fn, ok := r.deps.top(bd, name, []string{"function"}, true)
	if !ok {
		return outside
	}
	return typed{text: goResultText(fn.Signature), imp: &imp}
}

// tsImported is the type name that an import of a TypeScript file
// binds: in the imported module of the workspace, where its file prefix
// is not known, or outside the workspace.
func (r *refResolver) tsImported(imp store.Import, name string) recvType {
	if imp.ResolvedModuleID == 0 {
		return recvType{qualified: name, external: true, imp: &imp}
	}
	return recvType{qualified: name, scope: r.scopeOf(imp.ResolvedModuleID, "ts"), conf: ConfidenceImport, suffix: true}
}

// importedAs returns the name the source of a TypeScript import exports
// under the local name one of its named imports binds: the name itself,
// or a for import { a as local }.
func importedAs(imp store.Import, local string) (string, bool) {
	for _, n := range imp.Names {
		orig, alias, aliased := strings.Cut(n, "=")
		if aliased && alias == local || !aliased && orig == local {
			return orig, true
		}
	}
	return "", false
}

// blind reports whether a reference goes through a variable whose type
// the index cannot tell: one declared without an evident type, or with
// one that leads nowhere (the result of an unknown callee). What such a
// reference names is a guess.
func (r *refResolver) blind(ref store.Reference, fam string) bool {
	if ref.RecvType == "" {
		return false
	}
	_, _, ok := r.localRoot(ref, fam)
	return !ok
}

// tsAlias matches a TypeScript type alias of a plain named type, with
// or without type arguments, and captures that type.
var tsAlias = regexp.MustCompile(`^(?:export\s+)?type\s+[\w$]+(?:<.*>)?\s*=\s*([A-Za-z_$][\w$.]*)\s*(?:<.*>)?;?$`)

// maxAliases bounds the chain of type aliases followed for one type.
const maxAliases = 4

// tsNamed resolves a TypeScript type name written in the file of ref (T,
// or ns.T through a namespace import): a type an import of the file
// binds lies in the imported module, or outside the workspace when the
// import names none (see tsImported); else the type is one the file
// declares (see tsDeclared).
func (r *refResolver) tsNamed(ref store.Reference, expr string, depth int) (recvType, bool) {
	head, name, dotted := strings.Cut(expr, ".")
	if !dotted {
		name = expr
	}
	imps, err := r.fileImports(ref.FileID)
	if err != nil || strings.Contains(name, ".") {
		return recvType{}, false
	}
	for _, imp := range imps {
		if dotted && imp.Alias == head || !dotted && imp.Alias == name {
			return r.tsImported(imp, name), true
		}
		if orig, ok := importedAs(imp, name); ok && !dotted {
			return r.tsImported(imp, orig), true
		}
	}
	if dotted {
		return recvType{}, false
	}
	return r.tsDeclared(ref, name, depth)
}

// tsDeclared is the type name that the file of ref declares: an alias
// of a named type stands for that type (through at most maxAliases
// aliases). A name the file neither imports nor declares is a global,
// external to the workspace.
func (r *refResolver) tsDeclared(ref store.Reference, name string, depth int) (recvType, bool) {
	here := map[int64]bool{ref.ModuleID: true}
	qualified := ref.Enclosing[:strings.LastIndex(ref.Enclosing, "/")+1] + name
	decls, err := r.members(qualified, here)
	switch {
	case err != nil:
		return recvType{}, false
	case len(decls) == 0:
		return recvType{external: true}, true
	}
	if m := tsAlias.FindStringSubmatch(decls[0].Signature); m != nil && decls[0].Kind == "type" && depth < maxAliases {
		return r.tsNamed(ref, m[1], depth+1)
	}
	return recvType{qualified: qualified, scope: here, conf: ConfidenceLocal}, true
}

// Declarations of a Go package variable as its signature gives them:
// the name with a type (var x T, x *pkg.T), a composite literal or
// new(T), or a call whose first result it holds.
var (
	goVarTyped   = regexp.MustCompile(`^[A-Za-z_]\w*\s+(\*?[A-Za-z_][\w.]*(?:\[.*\])?)\s*(?:=.*)?$`)
	goVarLiteral = regexp.MustCompile(`^[A-Za-z_]\w*\s*=\s*&?\s*([A-Za-z_][\w.]*)(?:\[.*\])?\{`)
	goVarNew     = regexp.MustCompile(`^[A-Za-z_]\w*\s*=\s*new\(([A-Za-z_][\w.]*)\)`)
	goVarCall    = regexp.MustCompile(`^[A-Za-z_]\w*\s*=\s*([A-Za-z_]\w*(?:\.[A-Za-z_]\w*)?)\(`)
)

// goNoFunction holds what may precede "(" in an initializer without
// being a function with a signature: a function literal and the
// builtins that build a value.
var goNoFunction = map[string]bool{"func": true, "make": true, "append": true}

// goVarType returns what the signature of a Go variable makes evident
// of its type: the type name (T, pkg.T), or with call set the function
// whose first result it is. It is empty for any other declaration.
func goVarType(sig string) (expr string, call bool) {
	sig = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(sig), "var "))
	if m := goVarTyped.FindStringSubmatch(sig); m != nil {
		if t := goTypeName.FindStringSubmatch(m[1]); t != nil {
			return t[1], false
		}
		return "", false
	}
	for _, decl := range []*regexp.Regexp{goVarLiteral, goVarNew} {
		if m := decl.FindStringSubmatch(sig); m != nil {
			return m[1], false
		}
	}
	if m := goVarCall.FindStringSubmatch(sig); m != nil && !goNoFunction[m[1]] {
		return m[1], true
	}
	return "", false
}

// goPackageVar returns the type of the package-level variable name of
// the reference's package, declared in any of its files: the type its
// declaration names, resolved from the declaring file. ok is false when
// the package declares no such variable or its type is not evident.
func (r *refResolver) goPackageVar(ref store.Reference, name string) (recvType, bool) {
	vars, err := r.members(name, map[int64]bool{ref.ModuleID: true})
	if err != nil || len(vars) != 1 || vars[0].Kind != "var" {
		return recvType{}, false
	}
	expr, call := goVarType(vars[0].Signature)
	switch {
	case expr == "":
		return recvType{}, false
	case call:
		v, ok := r.goResult(vars[0], expr)
		if !ok {
			return recvType{}, false
		}
		return r.settle(v, "go")
	}
	return r.goNamed(vars[0], expr)
}

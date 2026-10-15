package resolve

import (
	"strings"

	"github.com/muralx/codedoc/internal/store"
)

// tsResult returns the declared result of the TypeScript function a
// call names, as its signature writes it: a function a named import of
// the reference's file brings in (from the workspace, or from a
// dependency deps.db holds), else one its file declares. ok is false
// when the callee is no such function or declares no result type.
func (r *refResolver) tsResult(ref store.Reference, callee string) (typed, bool) {
	imps, err := r.fileImports(ref.FileID)
	if err != nil || strings.Contains(callee, ".") {
		return typed{}, false
	}
	for _, imp := range imps {
		orig, named := importedAs(imp, callee)
		switch {
		case !named && imp.Alias != callee:
			continue
		case !named:
			return typed{}, false // a default or namespace import names no function by itself
		case imp.ResolvedModuleID == 0:
			return r.tsDepResult(imp, orig), true
		}
		return r.tsFunction(orig, r.scopeOf(imp.ResolvedModuleID, "ts"), "")
	}
	slash := strings.LastIndex(ref.Enclosing, "/")
	if slash < 0 {
		return typed{}, false
	}
	return r.tsFunction(callee, map[int64]bool{ref.ModuleID: true}, ref.Enclosing[:slash+1])
}

// tsFunction is tsResult for the one function of the workspace named
// name: in the file with the qualified name prefix, or without a prefix
// in any file of the modules an import leads to.
func (r *refResolver) tsFunction(name string, scope map[int64]bool, prefix string) (typed, bool) {
	fns, err := r.namedIn(name, scope)
	fns = filter(fns, func(t store.Target) bool {
		if t.Kind != "function" {
			return false
		}
		if prefix != "" {
			return t.Qualified == prefix+name
		}
		return t.Qualified == name || strings.HasSuffix(t.Qualified, "/"+name)
	})
	if err != nil || len(fns) != 1 {
		return typed{}, false
	}
	text := tsResultText(strings.TrimSpace(fns[0].Signature))
	return typed{text: text, at: fns[0], far: prefix == ""}, text != ""
}

// tsDepResult is tsResult for a function of a package outside the
// workspace: the type its result names in that package, or a type
// outside what is indexed when deps.db cannot tell.
func (r *refResolver) tsDepResult(imp store.Import, name string) typed {
	if !r.deps.ready() {
		return outside
	}
	bd, ok := r.deps.bind(imp, "ts")
	if !ok {
		return outside
	}
	fn, ok := r.deps.bareImport(bd, imp, name, []string{"function"}, "ts")
	if !ok {
		return outside
	}
	return typed{text: tsResultText(strings.TrimSpace(fn.Signature)), imp: &imp}
}

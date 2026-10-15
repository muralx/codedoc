package resolve

import (
	"strings"

	"github.com/muralx/codedoc/internal/store"
)

// pyBinds reports whether a Python import binds the reference and
// returns the module it binds. import a.b binds the qualifiers a.b and
// a.b.x; import a.b as c binds c. from m import x, y as z binds the bare
// names and qualifier heads x and z (a bare z stands for y); when such a
// name is a submodule of m (from pkg import mod), the binding is that
// submodule.
func (r *refResolver) pyBinds(imp store.Import, head string, ref store.Reference) binding {
	if imp.Names == nil {
		if imp.Alias != "" {
			return binding{module: imp.ResolvedModuleID, ok: ref.Qualifier != "" && head == imp.Alias}
		}
		ok := ref.Qualifier == imp.Raw || strings.HasPrefix(ref.Qualifier, imp.Raw+".")
		return binding{module: imp.ResolvedModuleID, ok: ok}
	}
	for _, n := range imp.Names {
		if b := r.pyNameBinds(imp, n, head, ref); b.ok {
			return b
		}
	}
	return binding{}
}

// pyNameBinds reports whether one name n (x or y=z) of a from import
// binds the reference; see pyBinds.
func (r *refResolver) pyNameBinds(imp store.Import, n, head string, ref store.Reference) binding {
	orig, local, aliased := strings.Cut(n, "=")
	if !aliased {
		local = orig
	}
	switch {
	case ref.Qualifier == "" && local == ref.Name:
		b := binding{module: imp.ResolvedModuleID, ok: true}
		if aliased {
			b.orig = orig
		}
		return b
	case ref.Qualifier != "" && head == local:
		if sub := r.submodule(imp.ResolvedModuleID, orig); sub != 0 {
			return binding{module: sub, ok: true}
		}
		return binding{module: imp.ResolvedModuleID, ok: true}
	}
	return binding{}
}

// submodule returns the child module of mod named name, or 0.
func (r *refResolver) submodule(mod int64, name string) int64 {
	want := r.mods[mod].Identity + "." + name
	for _, c := range r.children[mod] {
		if r.mods[c].Identity == want {
			return c
		}
	}
	return 0
}

// javaBinds reports whether a single-type (or static) Java import binds
// a bare name or a qualifier head: its last segment is that name.
// Wildcard imports bind nothing by name; see viaWildcard.
func javaBinds(imp store.Import, head string, ref store.Reference) bool {
	if imp.Alias == "" {
		return false
	}
	if ref.Qualifier == "" {
		return imp.Alias == ref.Name
	}
	return imp.Alias == head
}

// viaWildcard resolves a bare Java name through the wildcard imports of
// its file (import a.b.*): the candidates in the imported packages.
func (r *refResolver) viaWildcard(ref store.Reference, c candidates) outcome {
	in := map[int64]bool{}
	for _, imp := range r.imports[ref.FileID] {
		if strings.HasSuffix(imp.Raw, ".*") && imp.ResolvedModuleID != 0 {
			for id := range r.scopeOf(imp.ResolvedModuleID, "java") {
				in[id] = true
			}
		}
	}
	return pick(c.inModules(in), ConfidenceImport)
}

// scopeOf returns the modules a reference into mod may land in: for Java
// every directory of the same package in the same repo (main and test
// sources), else mod and its descendants.
func (r *refResolver) scopeOf(mod int64, fam string) map[int64]bool {
	if fam != "java" {
		return r.subtree(mod)
	}
	if set, ok := r.packages[mod]; ok {
		return set
	}
	m, set := r.mods[mod], map[int64]bool{mod: true}
	for id, o := range r.mods {
		if o.RepoID == m.RepoID && o.Lang == m.Lang && o.Identity == m.Identity {
			set[id] = true
		}
	}
	r.packages[mod] = set
	return set
}

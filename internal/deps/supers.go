package deps

import (
	"strings"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/resolve"
	"github.com/muralx/codedoc/internal/store"
)

// Super is a supertype a declaration of a package file names: the class
// or interface a TypeScript declaration extends, or the type a Go struct
// embeds. Inherited members are not copied (deps spec section 5); they
// are found through the extends edge a Super becomes (see linkSupers).
type Super struct {
	// Sub is the qualified name of the declaration in its file; Name and
	// Qualifier are the supertype as written (Server, ns.Base).
	Sub, Name, Qualifier string
	// From is the stem of the package file the name is imported from, ""
	// when the file does not import it from a file of the package.
	From string
	Line int
}

// containerKinds are the symbol kinds an extends edge may start from or
// end at.
var containerKinds = map[string]bool{"class": true, "interface": true, "struct": true}

// tsSupers turns the extend references of a declaration file into
// supertypes, each with the file its name is imported from: targets maps
// the relative import specifiers of the file to package files.
func tsSupers(res *extract.FileResult, targets map[string]string) []Super {
	from := map[string]string{}
	for _, imp := range res.Imports {
		next, ok := targets[imp.Raw]
		if !ok {
			continue
		}
		for _, n := range imp.Names {
			if _, local, aliased := strings.Cut(n, "="); aliased {
				n = local
			}
			from[n] = stem(next)
		}
		if imp.Alias != "" {
			from[imp.Alias] = stem(next)
		}
	}
	var out []Super
	for _, ref := range res.References {
		head, _, _ := strings.Cut(ref.Qualifier, ".")
		if head == "" {
			head = ref.Name
		}
		out = append(out, Super{Sub: ref.Enclosing, Name: ref.Name, Qualifier: ref.Qualifier, From: from[head],
			Line: ref.Line})
	}
	return out
}

// goSupers returns the types the structs of a Go file embed: a field
// named after its own type (Base, *Base). A type of another package
// (sync.Mutex) is not followed.
func goSupers(syms []extract.Symbol) []Super {
	var out []Super
	for _, sy := range syms {
		typ := strings.TrimPrefix(strings.TrimSpace(sy.SigText), "*")
		if generic := strings.IndexByte(typ, '['); generic > 0 {
			typ = typ[:generic]
		}
		if sy.Kind == extract.KindField && sy.Container != "" && typ == sy.Name {
			out = append(out, Super{Sub: sy.Container, Name: sy.Name, Line: sy.Span.StartLine})
		}
	}
	return out
}

// written is a symbol of the package being written.
type written struct {
	id, fileID int64
	module     int
	stem       string
	sym        extract.Symbol
}

// superIndex finds the declaration a Super names among the symbols of
// one package.
type superIndex struct {
	byName map[string][]written
}

// add records the container symbols of one written file.
func (x *superIndex) add(f File, fileID int64, ids map[string]int64, keys []string) {
	for i, sy := range f.Symbols {
		if containerKinds[string(sy.Kind)] {
			x.byName[sy.Name] = append(x.byName[sy.Name], written{id: ids[keys[i]], fileID: fileID, module: f.Module,
				stem: stem(f.Path), sym: sy})
		}
	}
}

// sub returns the declaration of a file with a qualified name.
func (x *superIndex) sub(f File, qualified string) (written, bool) {
	name := qualified[strings.LastIndexAny(qualified, "./")+1:]
	for _, w := range x.byName[name] {
		if w.stem == stem(f.Path) && w.sym.Qualified == qualified {
			return w, true
		}
	}
	return written{}, false
}

// named returns the declarations a supertype of sub may name: those
// with its name, other than sub, whose qualified name ends with the
// qualifier when there is one (ns.Base).
func (x *superIndex) named(sub written, sp Super) []written {
	var out []written
	for _, w := range x.byName[sp.Name] {
		q := w.sym.Qualified
		if w.id != sub.id && (sp.Qualifier == "" || strings.HasSuffix(q, "."+sp.Qualifier+"."+sp.Name) ||
			strings.HasSuffix(q, "/"+sp.Qualifier+"."+sp.Name)) {
			out = append(out, w)
		}
	}
	return out
}

// find resolves a supertype of sub to one declaration and the confidence
// it was found with: next to sub (the same container of the same file),
// in the file its name is imported from, in sub's module, else the only
// declaration of that name in the package. ok is false when nothing, or
// more than one declaration, is left.
func (x *superIndex) find(sub written, sp Super) (written, string, bool) {
	cands := x.named(sub, sp)
	tiers := []struct {
		conf string
		keep func(w written) bool
	}{
		{resolve.ConfidenceLocal, func(w written) bool { return w.stem == sub.stem && w.sym.Container == sub.sym.Container }},
		{resolve.ConfidenceImport, func(w written) bool { return sp.From != "" && w.stem == sp.From }},
		{resolve.ConfidenceLocal, func(w written) bool { return w.module == sub.module }},
		{resolve.ConfidenceUnique, func(written) bool { return true }},
	}
	for _, tier := range tiers {
		var in []written
		for _, w := range cands {
			if tier.keep(w) {
				in = append(in, w)
			}
		}
		if len(in) > 0 {
			return in[0], tier.conf, len(in) == 1
		}
	}
	return written{}, "", false
}

// linkSupers stores one extends edge per supertype of the package's
// files that names a declaration of the package: the only edges deps.db
// holds (deps spec section 5).
func linkSupers(b *store.Batch, files []File, x *superIndex) error {
	for _, f := range files {
		for _, sp := range f.Supers {
			sub, ok := x.sub(f, sp.Sub)
			if !ok {
				continue
			}
			super, conf, ok := x.find(sub, sp)
			if !ok {
				continue
			}
			err := b.InsertEdge(store.Edge{Src: sub.id, Dst: super.id, Kind: resolve.EdgeExtends, RefKind: "extend",
				Confidence: conf, FileID: sub.fileID, Line: sp.Line})
			if err != nil {
				return err
			}
		}
	}
	return nil
}

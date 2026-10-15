package resolve

import (
	"github.com/muralx/codedoc/internal/parse"
	"github.com/muralx/codedoc/internal/store"
)

// Module edge kind and confidence derived from resolved imports.
const (
	EdgeImports      = "imports"
	ConfidenceImport = "import"
)

// moduleEdges derives one imports edge per pair of importing module (the
// module of the importing file) and resolved module. A module importing
// itself gets no edge.
func (r *resolver) moduleEdges(imps []store.Import) ([]store.ModuleEdge, error) {
	seen := map[[2]int64]bool{}
	var edges []store.ModuleEdge
	for _, imp := range imps {
		if imp.ResolvedModuleID == 0 {
			continue
		}
		fam := parse.Family(imp.Lang)
		src, err := r.moduleAt(imp.RepoID, fam, ModulePath(fam, imp.Path))
		if err != nil {
			return nil, err
		}
		pair := [2]int64{src, imp.ResolvedModuleID}
		if src == 0 || src == imp.ResolvedModuleID || seen[pair] {
			continue
		}
		seen[pair] = true
		edges = append(edges, store.ModuleEdge{Src: src, Dst: imp.ResolvedModuleID, Kind: EdgeImports, Confidence: ConfidenceImport})
	}
	return edges, nil
}

// appendEdges appends the imports edges of source module src derived from
// the imports of the files in its directory; imports of files of another
// language family belong to another module and are skipped.
func appendEdges(edges []store.ModuleEdge, src store.Module, imps []store.Import) []store.ModuleEdge {
	seen := map[int64]bool{}
	for _, imp := range imps {
		dst := imp.ResolvedModuleID
		if dst == 0 || dst == src.ID || seen[dst] || parse.Family(imp.Lang) != src.Lang {
			continue
		}
		seen[dst] = true
		edges = append(edges, store.ModuleEdge{Src: src.ID, Dst: dst, Kind: EdgeImports, Confidence: ConfidenceImport})
	}
	return edges
}

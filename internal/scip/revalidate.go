package scip

import (
	"sort"

	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/store"
)

// revalidation is what a merge drops from its trusted files: the precise
// edges an earlier merge stored that this one did not confirm.
type revalidation struct {
	// edges are deleted; refs, rows an earlier merge added, are deleted
	// with their edges.
	edges, refs []int64
	// files and modules are queued for heuristic resolution: the
	// references of files, the Go implements edges of modules.
	files, modules []int64
}

// revalidate plans what the merge drops: every precise edge of a trusted
// file is kept only when this merge plans it again, by its reference row
// or, for an implements edge, by its pair of types.
func (m *merger) revalidate() error {
	ids := make([]int64, 0, len(m.paths))
	for _, p := range m.paths {
		ids = append(ids, m.files[p].ID)
	}
	prior, err := m.s.PreciseEdgesInFiles(ids)
	if err != nil {
		return err
	}
	refs := map[int64]bool{}
	for _, e := range m.edges {
		refs[e.ReferenceID] = true
	}
	pairs := map[[2]int64]bool{}
	for _, e := range m.impls {
		pairs[[2]int64{e.src, e.dst}] = true
	}
	files, modules := map[int64]bool{}, map[int64]bool{}
	for _, e := range prior {
		switch {
		case e.ReferenceID != 0 && refs[e.ReferenceID], e.Kind == "implements" && pairs[[2]int64{e.Src, e.Dst}]:
		case e.ReferenceID == 0:
			m.drop.edges = append(m.drop.edges, e.ID)
			modules[e.ModuleID] = true
		case e.Added:
			m.drop.refs = append(m.drop.refs, e.ReferenceID)
		default:
			m.drop.edges = append(m.drop.edges, e.ID)
			files[e.FileID] = true
		}
	}
	m.drop.files, m.drop.modules = sortedIDs(files), sortedIDs(modules)
	m.stats.Dropped = len(m.drop.edges) + len(m.drop.refs)
	return nil
}

// dropUnconfirmed deletes what revalidate planned and queues the
// resolution of what it uncovered.
func (m *merger) dropUnconfirmed(b *store.Batch) error {
	for _, id := range m.drop.refs {
		if err := b.DeleteReference(id); err != nil {
			return err
		}
	}
	for _, id := range m.drop.edges {
		if err := b.DeleteEdge(id); err != nil {
			return err
		}
	}
	if len(m.drop.files) == 0 && len(m.drop.modules) == 0 {
		return nil
	}
	return index.QueueResolution(b, m.drop.files, m.drop.modules)
}

func sortedIDs(set map[int64]bool) []int64 {
	out := make([]int64, 0, len(set))
	for id := range set {
		if id != 0 {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

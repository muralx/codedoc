package testutil

import "github.com/muralx/codedoc/internal/extract"

// WithoutCols returns refs with their columns zeroed, for tests that
// list expected references by line only (golden files check columns).
func WithoutCols(refs []extract.Reference) []extract.Reference {
	out := make([]extract.Reference, len(refs))
	for i, r := range refs {
		r.Col = 0
		out[i] = r
	}
	return out
}

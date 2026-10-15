package query

import (
	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/store"
)

// textKey names the lines of a document or section: its file and its
// line range.
type textKey struct {
	file       int64
	start, end int
}

// oncePerText drops the hits that are the same lines as another: a
// document and the section that is its whole text (the one heading at
// its top level) match the same query with the same lines. The section,
// the more specific address, is kept, at the place of the better ranked
// of the two, with the better score and the passages of both.
func oncePerText(order []hit, syms map[int64]store.Symbol, scores map[hit]float64, found passagesByHit) []hit {
	key := func(h hit, kind extract.Kind) (textKey, bool) {
		sy := syms[h.id]
		return textKey{sy.FileID, sy.StartLine, sy.EndLine}, !h.module && !h.dep && sy.Kind == string(kind)
	}
	sections := map[textKey]hit{}
	for _, h := range order {
		if k, ok := key(h, extract.KindSection); ok {
			sections[k] = h
		}
	}
	listed := map[hit]bool{}
	out := order[:0:0]
	for _, h := range order {
		if k, ok := key(h, extract.KindDocument); ok {
			if sec, twin := sections[k]; twin {
				scores[sec] = max(scores[sec], scores[h])
				found[sec] = append(found[sec], found[h]...)
				h = sec
			}
		}
		if !listed[h] {
			listed[h] = true
			out = append(out, h)
		}
	}
	return out
}

package query

import (
	"math"

	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/store"
)

// Passage is a line range of a file that matched a search, with its score.
type Passage struct {
	StartLine int     `json:"start"`
	EndLine   int     `json:"end"`
	Score     float64 `json:"score"`
}

// passageMargin is how far below a hit's best passage score another of
// its passages may be and still be reported with it: a long text has
// many passages, and only those about as close to the query as the best
// one matched it. Cosine scores of the model lie close together (0.55
// to 0.75 on real text), so the margin is absolute and small.
const passageMargin = 0.03

// rankedHit is a hit in its final rank. passages are the passages of its
// embedded text (doc, section text or body) closest to the query, best
// first, at most store.MaxHitPassages: the lines of the hit's file they
// cover and their cosine score. They are empty for a module and for a
// hit only the text indexes found, whose matching lines the caller finds
// itself.
type rankedHit struct {
	hit
	passages []Passage
	// score is the fused score the hit is ranked by (see fuse).
	score float64
}

// passagesByHit collects the passages of the symbol hits of the vector
// lists of a search.
type passagesByHit map[hit][]Passage

// record keeps the passages of a scanned row that matched about as well
// as its best one, rounding scores to four decimals.
func (p passagesByHit) record(h hit, scored []store.ScoredPassage) {
	for i, sp := range scored {
		if i > 0 && sp.Score < scored[0].Score-passageMargin {
			break
		}
		p[h] = append(p[h], Passage{StartLine: sp.StartLine, EndLine: sp.EndLine,
			Score: math.Round(sp.Score*1e4) / 1e4})
	}
}

// attach returns ranked hits with their recorded passages and fused
// scores.
func (p passagesByHit) attach(hits []hit, scores map[hit]float64) []rankedHit {
	out := make([]rankedHit, len(hits))
	for i, h := range hits {
		out[i] = rankedHit{hit: h, passages: p[h], score: scores[h]}
	}
	return out
}

// withPassages lists the passages of a hit in its entry's
// Extra["passages"], as {start, end, score} in JSON; an entry of a hit
// without passages is returned as it is.
func withPassages(e output.Entry, passages []Passage) output.Entry {
	if len(passages) == 0 {
		return e
	}
	extra := make(map[string]any, len(e.Extra)+1)
	for k, v := range e.Extra {
		extra[k] = v
	}
	extra["passages"] = passages
	e.Extra = extra
	return e
}

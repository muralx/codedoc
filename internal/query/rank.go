package query

import (
	"sort"
	"strings"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/store"
)

// Ranking of a search (spec 7.7). The text indexes and the vector scans
// each give several lists (symbols, section texts, modules, and the same
// for dependencies). Fusing them all by rank lets the first row of a
// weak list (a module whose doc holds one of five query words) stand
// next to the first row of a strong one, so they are first merged into
// two lists by what makes their rows comparable: text hits by how many
// query words they hold, vector hits by cosine. The two are then fused
// by reciprocal rank, and sections of one document spread apart.

// vecMargin is how far below the best cosine of a search a vector hit
// may score and still be ranked: the model puts unrelated text at 0.45
// to 0.6 and what a query is about within about 0.1 of the best, so a
// fixed depth would rank noise.
const vecMargin = 0.1

// cosineStep is the cosine distance from the best vector hit that
// counts as one rank in the fusion: a hit that leads the next by a clear
// gap is ahead by more than one place, and hits the model cannot tell
// apart share one.
const cosineStep = 0.005

// rankedList is a list to fuse: its hits, best first, and the rank of
// each from 0; without ranks a hit's rank is its place.
type rankedList struct {
	hits  []hit
	ranks []float64
}

// ordinal is the list whose hits rank by their place.
func ordinal(hits []hit) rankedList { return rankedList{hits: hits} }

func (l rankedList) rankOf(i int) float64 {
	if l.ranks == nil {
		return float64(i)
	}
	return l.ranks[i]
}

// scoredHit is a hit of a vector scan with its cosine score.
type scoredHit struct {
	hit
	score float64
}

// byCosine merges vector hits into one list, best first, without those
// more than vecMargin below the best; a hit's rank is its distance from
// the best in steps of cosineStep. Ties keep symbols before modules, the
// workspace before dependencies, then the lower id.
func byCosine(hits []scoredHit) rankedList {
	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.score != b.score {
			return a.score > b.score
		}
		return hitLess(a.hit, b.hit)
	})
	var out rankedList
	for _, h := range hits {
		if h.score < hits[0].score-vecMargin {
			break
		}
		out.hits = append(out.hits, h.hit)
		out.ranks = append(out.ranks, (hits[0].score-h.score)/cosineStep)
	}
	return out
}

// hitLess orders hits that tie: symbols, then the workspace, then the
// lower id.
func hitLess(a, b hit) bool {
	switch {
	case a.module != b.module:
		return !a.module
	case a.dep != b.dep:
		return !a.dep
	}
	return a.id < b.id
}

// byCoverage merges the lists of the text indexes into one: hits whose
// text holds more of the query terms first, then those ranked higher in
// their own list, then those of an earlier list. Hits holding fewer than
// half the terms of the best one are dropped, since with terms joined by
// OR a text index matches any row holding any one of them; those keep
// reports stay (the vector hits: what the model finds close to the query
// and a text index matched too is no accident, however few words they
// share).
func byCoverage(lists [][]hit, terms []string, textOf func(hit) string, keep func(hit) bool) []hit {
	type covered struct {
		hit
		terms, rank, list int
	}
	at := map[hit]int{}
	var all []covered
	best := 0
	for n, l := range lists {
		for rank, h := range l {
			if i, seen := at[h]; seen {
				all[i].rank = min(all[i].rank, rank)
				continue
			}
			at[h] = len(all)
			all = append(all, covered{hit: h, terms: overlap(textOf(h), terms), rank: rank, list: n})
			best = max(best, all[len(all)-1].terms)
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		a, b := all[i], all[j]
		switch {
		case a.terms != b.terms:
			return a.terms > b.terms
		case a.rank != b.rank:
			return a.rank < b.rank
		case a.list != b.list:
			return a.list < b.list
		}
		return hitLess(a.hit, b.hit)
	})
	var out []hit
	for _, c := range all {
		if 2*c.terms >= best || keep(c.hit) {
			out = append(out, c.hit)
		}
	}
	return out
}

// hitTexts returns the text each hit of lists is matched by: a symbol's
// name, qualified name, signature and doc, a section's own text, a
// module's path, identity and doc, as the text indexes hold them, and
// the query terms the names used in a symbol's body hold.
func (c *Ctx) hitTexts(lists [][]hit, uses bodyUses) (func(hit) string, error) {
	var all []hit
	for _, l := range lists {
		all = append(all, l...)
	}
	ws, err := loadRows(c.Store, all, false)
	if err != nil {
		return nil, err
	}
	var dep rows
	if c.view != nil {
		if dep, err = loadRows(c.view.s, all, true); err != nil {
			return nil, err
		}
	}
	var sections []int64
	for id, sy := range ws.syms {
		if extract.IsDocKind(sy.Kind) {
			sections = append(sections, id)
		}
	}
	own, err := c.Store.SectionTexts(sections)
	if err != nil {
		return nil, err
	}
	return func(h hit) string {
		r := ws
		if h.dep {
			r = dep
		}
		if h.module {
			m := r.mods[h.id]
			return strings.Join([]string{m.Path, m.Identity, m.DocText}, "\n")
		}
		sy := r.syms[h.id]
		text := strings.Join([]string{sy.Name, sy.Qualified, sy.Signature, sy.DocText}, "\n")
		if !h.dep {
			text += "\n" + own[h.id] + "\n" + strings.Join(uses.terms[h], " ")
		}
		return text
	}, nil
}

// spread moves a section that would directly follow another section of
// the same document behind the next hit from elsewhere, so the sections
// of one long document do not fill the first results. docOf is the file
// of a hit that is a document or section, 0 for any other hit.
func spread(order []hit, docOf func(hit) int64) []hit {
	out := make([]hit, 0, len(order))
	var held []hit
	follows := func(h hit) bool {
		d := docOf(h)
		return d != 0 && len(out) > 0 && docOf(out[len(out)-1]) == d
	}
	for _, h := range order {
		if follows(h) {
			held = append(held, h)
			continue
		}
		out = append(out, h)
		if len(held) > 0 && !follows(held[0]) {
			out = append(out, held[0])
			held = held[1:]
		}
	}
	return append(out, held...)
}

// testWeight scales the fused score of a hit in test code: a test names
// what it checks in words, so it matches a prose query as well as the
// code it tests, which is what was asked for.
const testWeight = 0.75

// fuse merges ranked lists by reciprocal rank fusion: a hit scores the
// sum of 1/(rrfK + rank) over the lists it appears in (rank from 1; see
// rankedList), times testWeight when isTest reports it; the scores are
// returned with the order.
// Ties keep production code before the hits isTest reports, then the hit
// with the better best rank, then symbols, then the workspace, then the
// lower id.
func fuse(isTest func(hit) bool, lists ...rankedList) ([]hit, map[hit]float64) {
	score := map[hit]float64{}
	best := map[hit]float64{}
	var order []hit
	for _, l := range lists {
		for i, h := range l.hits {
			rank := l.rankOf(i)
			if _, seen := score[h]; !seen {
				order = append(order, h)
				best[h] = rank
			}
			score[h] += 1 / (rrfK + rank + 1)
			best[h] = min(best[h], rank)
		}
	}
	for _, h := range order {
		if isTest(h) {
			score[h] *= testWeight
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := order[i], order[j]
		switch {
		case score[a] != score[b]:
			return score[a] > score[b]
		case isTest(a) != isTest(b):
			return isTest(b)
		case best[a] != best[b]:
			return best[a] < best[b]
		case a.module != b.module:
			return !a.module
		case a.dep != b.dep:
			return !a.dep
		}
		return a.id < b.id
	})
	return order, score
}

// stopWords are the words of a query that say nothing about what is
// searched for.
var stopWords = map[string]bool{"a": true, "an": true, "the": true, "to": true, "of": true, "in": true, "on": true,
	"at": true, "for": true, "is": true, "are": true, "was": true, "be": true, "by": true, "with": true, "and": true,
	"or": true, "how": true, "what": true, "where": true, "when": true, "which": true, "who": true, "why": true,
	"does": true, "do": true, "it": true, "its": true, "this": true, "that": true, "from": true, "as": true,
	"into": true, "there": true, "can": true}

// searchTerms returns what a query is matched by in the text indexes:
// its words (see queryWords) without stop words, unless it has no other,
// each cut to its stem (see stem), which matches as a prefix.
func searchTerms(text string) []string {
	words := queryWords(text)
	var out []string
	seen := map[string]bool{}
	for _, w := range words {
		if s := stem(w); !stopWords[w] && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return words
	}
	return out
}

// stem cuts the ending of an inflected English word (checked, agents,
// polling), so that as a prefix it matches the other forms; a word whose
// stem would have fewer than four letters stays whole.
func stem(w string) string {
	for _, suffix := range []string{"ing", "ed", "es", "s"} {
		if strings.HasSuffix(w, suffix) && len(w)-len(suffix) >= 4 && !strings.HasSuffix(w, "ss") {
			return w[:len(w)-len(suffix)]
		}
	}
	return w
}

// docFiles returns, for the workspace symbol hits that are documents or
// sections, the file they are in.
func docFiles(syms map[int64]store.Symbol) func(hit) int64 {
	return func(h hit) int64 {
		if sy := syms[h.id]; !h.module && !h.dep && extract.IsDocKind(sy.Kind) {
			return sy.FileID
		}
		return 0
	}
}

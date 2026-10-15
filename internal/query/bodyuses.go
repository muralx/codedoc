package query

import "github.com/muralx/codedoc/internal/store"

// bodyUses are the workspace symbols whose bodies use a name that holds
// a query term (store.ReferenceMatches): the keyword hits on code the
// text indexes of names, signatures and docs cannot give.
type bodyUses struct {
	// list ranks them, those using names of more terms first.
	list []hit
	// terms are the query terms the names a hit uses hold; lines the
	// file lines of those uses.
	terms map[hit][]string
	lines map[hit][]int
}

// bodyUses finds the symbols whose bodies use names of the query terms;
// none when only modules are searched.
func (c *Ctx) bodyUses(terms []string, f store.SearchFilter, kind string) (bodyUses, error) {
	u := bodyUses{terms: map[hit][]string{}, lines: map[hit][]int{}}
	if kind == "module" {
		return u, nil
	}
	matches, err := c.Store.ReferenceMatches(terms, f, searchDepth)
	for _, m := range matches {
		h := hit{id: m.ID}
		u.list = append(u.list, h)
		u.terms[h] = m.Terms
		u.lines[h] = m.Lines
	}
	return u, err
}

// fill gives the hits no vector passage was recorded for the lines of
// their matching uses as passages without a score, the first
// store.MaxHitPassages of them: the lines of the body that matched.
func (u bodyUses) fill(found passagesByHit) {
	for h, lines := range u.lines {
		if len(found[h]) > 0 {
			continue
		}
		for _, l := range lines[:min(len(lines), store.MaxHitPassages)] {
			found[h] = append(found[h], Passage{StartLine: l, EndLine: l})
		}
	}
}

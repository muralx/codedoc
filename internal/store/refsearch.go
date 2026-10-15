package store

import (
	"database/sql"
	"slices"
	"sort"
	"strings"
)

// RefMatch is a symbol whose body uses names that match search terms:
// the calls and other references the extractor recorded inside it.
type RefMatch struct {
	ID int64
	// Names are the distinct matching names used, lower-cased, sorted.
	Names []string
	// Lines are the file lines of the matching uses, ascending.
	Lines []int
	// Terms are the search terms some name used holds, in their order.
	Terms []string
}

// containsFrom is the length from which a search term matches anywhere
// in a name (hub in spawnHubProcess); a shorter one matches as a prefix.
const containsFrom = 3

// ReferenceMatches returns the symbols whose bodies use a name matching
// one of terms (lower-cased words), ignoring case: those using names of
// more terms first, then those with more matching uses, at most k. It
// finds a word that occurs in a body only, which the text indexes of
// names, signatures and docs do not hold.
func (s *Store) ReferenceMatches(terms []string, f SearchFilter, k int) ([]RefMatch, error) {
	if len(terms) == 0 || k <= 0 {
		return nil, nil
	}
	where, args := symbolFilter(f)
	like := make([]string, len(terms))
	patterns := make([]any, len(terms))
	for i, t := range terms {
		like[i] = `r.name LIKE ? ESCAPE '\'`
		patterns[i] = escapeLike(t) + "%"
		if len(t) >= containsFrom {
			patterns[i] = "%" + escapeLike(t) + "%"
		}
	}
	rows, err := s.db.Query(`SELECT r.symbol_id, r.line, lower(r.name) FROM reference r
  JOIN symbol s ON s.id = r.symbol_id JOIN file f ON f.id = s.file_id
  WHERE (`+strings.Join(like, " OR ")+`) AND `+unparkedSQL+where+` ORDER BY r.symbol_id, r.line`,
		append(patterns, args...)...)
	if err != nil {
		return nil, err
	}
	out, err := readRefMatches(rows)
	if err != nil {
		return nil, err
	}
	return bestRefMatches(out, terms, k), nil
}

// readRefMatches groups the rows of matching uses, ordered by symbol and
// line, by symbol.
func readRefMatches(rows *sql.Rows) ([]RefMatch, error) {
	var out []RefMatch
	for rows.Next() {
		var id int64
		var line int
		var name string
		if err := rows.Scan(&id, &line, &name); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if len(out) == 0 || out[len(out)-1].ID != id {
			out = append(out, RefMatch{ID: id})
		}
		m := &out[len(out)-1]
		if !slices.Contains(m.Names, name) {
			m.Names = append(m.Names, name)
		}
		if len(m.Lines) == 0 || m.Lines[len(m.Lines)-1] != line {
			m.Lines = append(m.Lines, line)
		}
	}
	return out, closeRows(rows)
}

// bestRefMatches orders matches by the terms their names hold, then by
// their matching lines, then by id, and returns the first k.
func bestRefMatches(out []RefMatch, terms []string, k int) []RefMatch {
	for i := range out {
		sort.Strings(out[i].Names)
		for _, t := range terms {
			if slices.ContainsFunc(out[i].Names, func(n string) bool { return strings.Contains(n, t) }) {
				out[i].Terms = append(out[i].Terms, t)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch {
		case len(a.Terms) != len(b.Terms):
			return len(a.Terms) > len(b.Terms)
		case len(a.Lines) != len(b.Lines):
			return len(a.Lines) > len(b.Lines)
		}
		return a.ID < b.ID
	})
	return out[:min(k, len(out))]
}

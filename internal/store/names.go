package store

import "strings"

// SymbolNames returns the distinct names of the symbols of the given
// repos, or of every repo without any, sorted: what an address that
// matches nothing is compared with to offer the nearest names.
func (s *Store) SymbolNames(repoIDs ...int64) ([]string, error) {
	where, args := "", make([]any, len(repoIDs))
	if len(repoIDs) > 0 {
		for i, id := range repoIDs {
			args[i] = id
		}
		where = ` JOIN file f ON f.id = s.file_id WHERE f.repo_id IN (?` + strings.Repeat(", ?", len(repoIDs)-1) + `)`
	}
	rows, err := s.db.Query(`SELECT DISTINCT s.name FROM symbol s`+where+` ORDER BY s.name`, args...)
	if err != nil {
		return nil, err
	}
	return scanStrings(rows)
}

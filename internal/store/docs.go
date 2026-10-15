package store

// MarkDocReviewed records that a symbol's doc was reviewed against its
// current body (an explicit doc set): its doc body hash becomes its body
// hash, so the doc is ok until the body changes again. A symbol without
// a doc is left alone.
func (s *Store) MarkDocReviewed(id int64) error {
	_, err := s.db.Exec(`UPDATE symbol SET doc_body_hash = body_hash, doc_state = 'ok'
  WHERE id = ? AND doc_hash IS NOT NULL`, id)
	return err
}

// DocCounts are the doc states of a repo's symbols (tests excluded) and
// modules, counted by state.
type DocCounts struct {
	Symbols, Modules map[string]int
}

// RepoDocCounts counts the doc states of a repo's non-test code symbols
// (documents and sections have no doc to keep) and of its modules.
func (s *Store) RepoDocCounts(repoID int64) (DocCounts, error) {
	out := DocCounts{Symbols: map[string]int{}, Modules: map[string]int{}}
	for q, m := range map[string]map[string]int{
		`SELECT s.doc_state, COUNT(*) FROM symbol s JOIN file f ON f.id = s.file_id
  WHERE f.repo_id = ? AND s.is_test = 0 AND ` + codeKindSQL + ` GROUP BY s.doc_state`: out.Symbols,
		`SELECT doc_state, COUNT(*) FROM module WHERE repo_id = ? GROUP BY doc_state`: out.Modules,
	} {
		rows, err := s.db.Query(q, repoID)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var st string
			var n int
			if err := rows.Scan(&st, &n); err != nil {
				_ = rows.Close()
				return out, err
			}
			m[st] = n
		}
		if err := closeRows(rows); err != nil {
			return out, err
		}
	}
	return out, nil
}

// RepoSymbols returns the symbols of a repo's files of a precise
// language, ordered by path and line.
func (s *Store) RepoSymbols(repoID int64, lang string) ([]Symbol, error) {
	return querySymbols(s.db, `WHERE f.repo_id = ? AND f.lang = ?`+symbolOrder, repoID, lang)
}

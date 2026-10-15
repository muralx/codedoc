package store

// SymbolMatches returns the ids of the symbols whose name, qualified
// name, signature or doc match an FTS5 query, best BM25 first, at most k.
// Name matches weigh more than doc matches.
func (s *Store) SymbolMatches(match string, f SearchFilter, k int) ([]int64, error) {
	where, args := symbolFilter(f)
	return s.matchIDs(`SELECT s.id FROM symbol_fts JOIN symbol s ON s.id = symbol_fts.rowid
  JOIN file f ON f.id = s.file_id
  WHERE symbol_fts MATCH ? AND substr(s.stable_key, 1, 1) <> char(1)`+where+`
  ORDER BY bm25(symbol_fts, 4.0, 2.0, 1.0, 1.0), s.id LIMIT ?`, match, args, k)
}

// ModuleMatches returns the ids of the modules whose path, identity or
// doc match an FTS5 query, best BM25 first, at most k; f.Kind is ignored.
func (s *Store) ModuleMatches(match string, f SearchFilter, k int) ([]int64, error) {
	where, args := moduleFilter(f)
	return s.matchIDs(`SELECT m.id FROM module_fts JOIN module m ON m.id = module_fts.rowid
  WHERE module_fts MATCH ?`+where+` ORDER BY bm25(module_fts), m.id LIMIT ?`, match, args, k)
}

func (s *Store) matchIDs(query, match string, args []any, k int) ([]int64, error) {
	if match == "" || k <= 0 {
		return nil, nil
	}
	rows, err := s.db.Query(query, append(append([]any{match}, args...), k)...)
	if err != nil {
		return nil, err
	}
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, id)
	}
	return out, closeRows(rows)
}

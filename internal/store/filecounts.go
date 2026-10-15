package store

// FileCount is a file row with the number of its symbols.
type FileCount struct {
	File
	Symbols int
}

// SymbolCountsByFile returns the files of a repo whose path starts with
// dirPrefix ("" for all), ordered by path, each with its symbol count.
func (s *Store) SymbolCountsByFile(repoID int64, dirPrefix string) ([]FileCount, error) {
	rows, err := s.db.Query(`SELECT `+fileColumns+`, n FROM file JOIN (SELECT f.id AS fid, COUNT(s.id) AS n
    FROM file f LEFT JOIN symbol s ON s.file_id = f.id
    WHERE f.repo_id = ? AND substr(f.path, 1, length(?)) = ? GROUP BY f.id) ON fid = file.id
  ORDER BY path`, repoID, dirPrefix, dirPrefix)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []FileCount
	for rows.Next() {
		var fc FileCount
		f := &fc.File
		if err := rows.Scan(&f.ID, &f.RepoID, &f.Path, &f.Lang, &f.Size, &f.Mtime, &f.Hash, &f.Lines, &f.Generated,
			&f.ParseErrors, &f.LastCommit, &f.LastCommitAt, &f.ExtractedVersion, &f.Title, &fc.Symbols); err != nil {
			return nil, err
		}
		out = append(out, fc)
	}
	return out, rows.Err()
}

// FileTotal returns the number of file rows of the workspace.
func (s *Store) FileTotal() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM file`).Scan(&n)
	return n, err
}

// ParseErrors returns the parse errors recorded on the files of a repo
// (spec 15), summed.
func (s *Store) ParseErrors(repoID int64) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COALESCE(SUM(parse_errors), 0) FROM file WHERE repo_id = ?`, repoID).Scan(&n)
	return n, err
}

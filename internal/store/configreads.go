package store

// KeyRead is a config key code reads on a line of a file. Defined is
// false when no indexed config file defines the key (an environment
// variable set outside the repo).
type KeyRead struct {
	Key     string
	Line    int
	Defined bool
}

// ConfigReadsIn returns the config keys read on the lines first to last
// of a file (the span of a symbol), each key once per line, ordered by
// line and key: the reads matched to a defined key, and the reads
// through an accessor, or of a constant's value, that no defined key
// matched.
func (s *Store) ConfigReadsIn(fileID int64, first, last int) ([]KeyRead, error) {
	rows, err := s.db.Query(`SELECT k.key, u.line, 1 FROM config_use u JOIN config_key k ON k.id = u.key_id
  WHERE u.file_id = ?1 AND u.kind = 'reads' AND u.line BETWEEN ?2 AND ?3
  UNION SELECT COALESCE(r.config_key, r.name), r.line, 0 FROM reference r
  WHERE r.file_id = ?1 AND r.kind = 'config' AND r.line BETWEEN ?2 AND ?3
  AND (r.qualifier = ?4 OR r.config_key IS NOT NULL)
  AND NOT EXISTS (SELECT 1 FROM config_use u WHERE u.reference_id = r.id)
  ORDER BY 2, 1`, fileID, first, last, ConfigAccessor)
	if err != nil {
		return nil, err
	}
	var out []KeyRead
	for rows.Next() {
		var r KeyRead
		if err := rows.Scan(&r.Key, &r.Line, &r.Defined); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, r)
	}
	return out, closeRows(rows)
}

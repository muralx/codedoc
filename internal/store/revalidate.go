package store

// PreciseEdge is a precise edge with what revalidating it needs.
type PreciseEdge struct {
	ID, Src, Dst, FileID int64
	// ReferenceID is zero for an edge detected without a reference.
	ReferenceID int64
	// ModuleID is the module of the source symbol.
	ModuleID int64
	Kind     string
	// Added is set when the edge's reference row was added by a merge.
	Added bool
}

// PreciseEdgesInFiles returns the precise edges recorded in the files
// fileIDs, ordered by id.
func (s *Store) PreciseEdgesInFiles(fileIDs []int64) ([]PreciseEdge, error) {
	if len(fileIDs) == 0 {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT e.id, e.src_symbol_id, COALESCE(e.dst_symbol_id,0), e.file_id,
  COALESCE(e.reference_id,0), COALESCE(s.module_id,0), e.kind, COALESCE(r.added,0)
  FROM edge e JOIN symbol s ON s.id = e.src_symbol_id LEFT JOIN reference r ON r.id = e.reference_id
  WHERE e.confidence = ? AND e.file_id IN (SELECT value FROM json_each(?)) ORDER BY e.id`,
		ConfidencePrecise, idList(fileIDs))
	if err != nil {
		return nil, err
	}
	var out []PreciseEdge
	for rows.Next() {
		var e PreciseEdge
		if err := rows.Scan(&e.ID, &e.Src, &e.Dst, &e.FileID, &e.ReferenceID, &e.ModuleID, &e.Kind,
			&e.Added); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, e)
	}
	return out, closeRows(rows)
}

// DeleteEdge deletes one edge with its candidates.
func (b *Batch) DeleteEdge(id int64) error {
	_, err := (cached{b}).Exec(`DELETE FROM edge WHERE id = ?`, id)
	return err
}

// DeleteReference deletes one reference row with the edges derived from
// it.
func (b *Batch) DeleteReference(id int64) error {
	_, err := (cached{b}).Exec(`DELETE FROM reference WHERE id = ?`, id)
	return err
}

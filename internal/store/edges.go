package store

// ModuleEdge is a module_edge row.
type ModuleEdge struct {
	Src, Dst         int64
	Kind, Confidence string
}

// ReplaceModuleEdges replaces every module edge of the workspace (one
// database holds one workspace) with edges.
func (b *Batch) ReplaceModuleEdges(edges []ModuleEdge) error {
	if _, err := b.tx.Exec(`DELETE FROM module_edge`); err != nil {
		return err
	}
	return b.insertModuleEdges(edges)
}

// ReplaceModuleEdgesFrom replaces the edges leaving the modules srcs with
// edges, whose sources must be among srcs; other edges are kept.
func (b *Batch) ReplaceModuleEdgesFrom(srcs []int64, edges []ModuleEdge) error {
	for lo := 0; lo < len(srcs); lo += maxParams {
		chunk := srcs[lo:min(lo+maxParams, len(srcs))]
		if _, err := b.tx.Exec(`DELETE FROM module_edge WHERE src_module_id IN (`+placeholders(len(chunk))+`)`,
			anys(chunk)...); err != nil {
			return err
		}
	}
	return b.insertModuleEdges(edges)
}

func (b *Batch) insertModuleEdges(edges []ModuleEdge) error {
	for _, e := range edges {
		if _, err := b.tx.Exec(`INSERT OR IGNORE INTO module_edge(src_module_id, dst_module_id, kind, confidence)
  VALUES(?, ?, ?, ?)`, e.Src, e.Dst, e.Kind, e.Confidence); err != nil {
			return err
		}
	}
	return nil
}

// ModuleEdgesFrom returns the edges leaving a module ordered by target.
func (s *Store) ModuleEdgesFrom(moduleID int64) ([]ModuleEdge, error) {
	rows, err := s.db.Query(`SELECT src_module_id, dst_module_id, kind, confidence FROM module_edge
  WHERE src_module_id = ? ORDER BY dst_module_id, kind`, moduleID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ModuleEdge
	for rows.Next() {
		var e ModuleEdge
		if err := rows.Scan(&e.Src, &e.Dst, &e.Kind, &e.Confidence); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

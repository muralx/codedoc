package store

// SymbolsByID returns the symbols with the given ids by id; ids without
// a row are left out.
func (s *Store) SymbolsByID(ids []int64) (map[int64]Symbol, error) {
	out := make(map[int64]Symbol, len(ids))
	err := inChunks(ids, func(chunk []any) error {
		syms, err := querySymbols(s.db, `WHERE s.id IN (`+placeholders(len(chunk))+`)`, chunk...)
		for _, sy := range syms {
			out[sy.ID] = sy
		}
		return err
	})
	return out, err
}

// ModulesByID returns the modules with the given ids by id; ids without
// a row are left out.
func (s *Store) ModulesByID(ids []int64) (map[int64]Module, error) {
	out := make(map[int64]Module, len(ids))
	err := inChunks(ids, func(chunk []any) error {
		mods, err := queryModules(s.db, `WHERE id IN (`+placeholders(len(chunk))+`)`, chunk...)
		for _, m := range mods {
			out[m.ID] = m
		}
		return err
	})
	return out, err
}

// EdgesToModule returns the edges of the given kinds that end at a
// symbol of a module, including the ambiguous edges listing one as a
// candidate, ordered by id.
func (s *Store) EdgesToModule(moduleID int64, kinds ...string) ([]Edge, error) {
	in := placeholders(len(kinds))
	args := append(append(append([]any{moduleID}, anys(kinds)...), moduleID), anys(kinds)...)
	return s.queryEdges(`SELECT `+edgeColumns+` FROM edge e JOIN symbol d ON d.id = e.dst_symbol_id
  WHERE d.module_id = ? AND e.kind IN (`+in+`)
  UNION SELECT `+edgeColumns+` FROM edge_candidate c JOIN symbol d ON d.id = c.symbol_id JOIN edge e ON e.id = c.edge_id
  WHERE d.module_id = ? AND e.kind IN (`+in+`) ORDER BY 1`, args...)
}

// OverCapReferences counts the references of the given kinds named name
// left unresolved because they had more candidates than the cap.
func (s *Store) OverCapReferences(name string, kinds ...string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM reference WHERE name = ? AND candidates > 0 AND kind IN (`+
		placeholders(len(kinds))+`)`, append([]any{name}, anys(kinds)...)...).Scan(&n)
	return n, err
}

// AmbiguousEdgesTo counts the ambiguous edges listing a symbol as a
// candidate.
func (s *Store) AmbiguousEdgesTo(id int64) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM edge_candidate WHERE symbol_id = ?`, id).Scan(&n)
	return n, err
}

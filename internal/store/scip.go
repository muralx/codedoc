package store

// ConfidencePrecise is the confidence of an edge taken from a SCIP index
// (spec 7.4). Heuristic resolution keeps precise edges: they go away
// only when the reference row they come from is rewritten, or, for
// implements edges detected without a reference, when the file of the
// implementing type is rewritten or its package's method sets change.
const ConfidencePrecise = "precise"

// SCIPSymbol is a scip_symbol row joined with the symbol it maps to.
type SCIPSymbol struct {
	StableKey, SCIP string
	// SymbolID, Kind and Name are read only, from the symbol row.
	SymbolID   int64
	Kind, Name string
}

// SCIPSymbols returns every scip_symbol row whose stable key names a
// symbol of the workspace, ordered by stable key.
func (s *Store) SCIPSymbols() ([]SCIPSymbol, error) {
	rows, err := s.db.Query(`SELECT m.stable_key, m.scip_symbol, s.id, s.kind, s.name FROM scip_symbol m
  JOIN symbol s ON s.stable_key = m.stable_key ORDER BY m.stable_key`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []SCIPSymbol
	for rows.Next() {
		var m SCIPSymbol
		if err := rows.Scan(&m.StableKey, &m.SCIP, &m.SymbolID, &m.Kind, &m.Name); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ReplaceSCIPSymbols deletes the scip_symbol rows whose stable key
// starts with one of prefixes and stores rows.
func (b *Batch) ReplaceSCIPSymbols(prefixes []string, rows []SCIPSymbol) error {
	for _, p := range prefixes {
		if _, err := b.tx.Exec(`DELETE FROM scip_symbol WHERE substr(stable_key, 1, length(?)) = ?`, p, p); err != nil {
			return err
		}
	}
	for _, m := range rows {
		if _, err := (cached{b}).Exec(`INSERT OR REPLACE INTO scip_symbol(stable_key, scip_symbol) VALUES(?, ?)`,
			m.StableKey, m.SCIP); err != nil {
			return err
		}
	}
	return nil
}

// CodeReferencesInFile returns the references of a file that sit inside
// a symbol and are not config references, ordered by line and id.
func (s *Store) CodeReferencesInFile(fileID int64) ([]Reference, error) {
	return queryReferences(s.db, `WHERE r.file_id = ? AND r.symbol_id IS NOT NULL AND r.kind <> 'config'
  ORDER BY r.line, r.id`, fileID)
}

// InsertReference stores one reference row a SCIP merge added (marked
// Added) and returns its id.
func (b *Batch) InsertReference(r Reference) (int64, error) {
	var id int64
	err := cached{b}.QueryRow(`INSERT INTO reference(file_id, symbol_id, kind, name, qualifier, line, col, arity, added)
  VALUES(?, ?, ?, ?, ?, ?, ?, ?, 1) RETURNING id`, r.FileID, nullID(r.SymbolID), r.Kind, r.Name, r.Qualifier, r.Line,
		r.Col, r.Arity).Scan(&id)
	return id, err
}

// ReplaceReferenceEdges replaces the edges of the reference e.ReferenceID
// (any confidence, candidates included) with e and clears the
// reference's recorded candidate count.
func (b *Batch) ReplaceReferenceEdges(e Edge) error {
	if _, err := (cached{b}).Exec(`DELETE FROM edge WHERE reference_id = ?`, e.ReferenceID); err != nil {
		return err
	}
	if _, err := (cached{b}).Exec(`UPDATE reference SET candidates = 0 WHERE id = ? AND candidates <> 0`,
		e.ReferenceID); err != nil {
		return err
	}
	return b.InsertEdge(e)
}

// SetPreciseImplements records that src implements dst for certain. An
// implements edge between the pair resolved from a reference (an
// implements clause) becomes precise; edges between the pair detected
// without a reference are replaced by one precise edge on fileID:line
// unless such a reference edge exists.
func (b *Batch) SetPreciseImplements(src, dst, fileID int64, line int) error {
	res, err := b.tx.Exec(`UPDATE edge SET confidence = ? WHERE src_symbol_id = ? AND dst_symbol_id = ?
  AND kind = 'implements' AND reference_id IS NOT NULL`, ConfidencePrecise, src, dst)
	if err != nil {
		return err
	}
	if _, err := b.tx.Exec(`DELETE FROM edge WHERE src_symbol_id = ? AND dst_symbol_id = ? AND kind = 'implements'
  AND reference_id IS NULL`, src, dst); err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n > 0 {
		return err
	}
	return b.InsertEdge(Edge{Src: src, Dst: dst, Kind: "implements", RefKind: "implement",
		Confidence: ConfidencePrecise, FileID: fileID, Line: line})
}

// SetSCIPCommit records the commit the repo's SCIP index was built from.
func (b *Batch) SetSCIPCommit(repoID int64, commit string) error {
	return requireOne(b.tx.Exec(`UPDATE repo SET scip_commit = ? WHERE id = ?`, commit, repoID))
}

// DeleteHeuristicEdges deletes every symbol edge of the workspace except
// the precise ones and the links between documents, which no reference
// resolution derives (see DeleteLinkEdges).
func (b *Batch) DeleteHeuristicEdges() error {
	_, err := b.tx.Exec(`DELETE FROM edge WHERE confidence <> ? AND ref_kind <> ?`, ConfidencePrecise, LinkRefKind)
	return err
}

// PreciseReferences returns which of the references ids have a precise
// edge; with ids nil, every such reference of the workspace.
func (b *Batch) PreciseReferences(ids []int64) (map[int64]bool, error) {
	out := map[int64]bool{}
	collect := func(where string, args ...any) error {
		rows, err := b.tx.Query(`SELECT DISTINCT reference_id FROM edge WHERE confidence = ? AND reference_id IS NOT NULL`+
			where, append([]any{ConfidencePrecise}, args...)...)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return err
			}
			out[id] = true
		}
		return rows.Err()
	}
	if ids == nil {
		return out, collect("")
	}
	err := inChunks(ids, func(chunk []any) error {
		return collect(` AND reference_id IN (`+placeholders(len(chunk))+`)`, chunk...)
	})
	return out, err
}

// PreciseImplements returns the (implementing, implemented) symbol pairs
// joined by a precise implements edge.
func (b *Batch) PreciseImplements() (map[[2]int64]bool, error) {
	rows, err := b.tx.Query(`SELECT src_symbol_id, dst_symbol_id FROM edge WHERE kind = 'implements'
  AND confidence = ? AND dst_symbol_id IS NOT NULL`, ConfidencePrecise)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[[2]int64]bool{}
	for rows.Next() {
		var p [2]int64
		if err := rows.Scan(&p[0], &p[1]); err != nil {
			return nil, err
		}
		out[p] = true
	}
	return out, rows.Err()
}

// DropPreciseDetected deletes the precise edges of a file stored without
// a reference (SCIP implements edges on its type declarations), whose
// file is about to be rewritten, and returns how many it deleted.
func (b *Batch) DropPreciseDetected(fileID int64) (int64, error) {
	res, err := (cached{b}).Exec(`DELETE FROM edge WHERE file_id = ? AND reference_id IS NULL AND confidence = ?`,
		fileID, ConfidencePrecise)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

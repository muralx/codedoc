package store

// Edge is an edge row between symbols. Dst is zero for an ambiguous edge,
// whose candidates are listed in Candidates (edge_candidate rows), and
// for an edge into a third-party package, whose target is DepKey: the
// stable key of a symbol of deps.db (deps spec section 6).
// ReferenceID is the reference the edge was resolved from, zero for an
// edge detected without one (Go implements).
type Edge struct {
	ID, Src, Dst              int64
	Kind, RefKind, Confidence string
	FileID                    int64
	Line                      int
	ReferenceID               int64
	Candidates                []int64
	DepKey                    string
}

// InsertEdge stores an edge and its candidates. The id is read back
// with LastInsertId, and only for candidates: RETURNING costs a result
// row per insert.
func (b *Batch) InsertEdge(e Edge) error {
	res, err := cached{b}.Exec(`INSERT INTO edge(src_symbol_id, dst_symbol_id, kind, ref_kind, confidence, file_id, line,
    reference_id, dst_dep_key) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`, e.Src, nullID(e.Dst), e.Kind, e.RefKind, e.Confidence,
		e.FileID, e.Line, nullID(e.ReferenceID), nullStr(e.DepKey))
	if err != nil || len(e.Candidates) == 0 {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	for _, c := range e.Candidates {
		if _, err := (cached{b}).Exec(`INSERT INTO edge_candidate(edge_id, symbol_id) VALUES(?, ?)`, id, c); err != nil {
			return err
		}
	}
	return nil
}

// DeleteReferenceEdges deletes the edges resolved from the references ids.
func (b *Batch) DeleteReferenceEdges(ids []int64) error {
	return inChunks(ids, func(chunk []any) error {
		_, err := b.tx.Exec(`DELETE FROM edge WHERE reference_id IN (`+placeholders(len(chunk))+`)`, chunk...)
		return err
	})
}

// DeleteDetectedEdges deletes the edges of a kind stored without a
// reference whose source or target symbol lies in one of the modules.
func (b *Batch) DeleteDetectedEdges(kind string, moduleIDs []int64) error {
	return inChunks(moduleIDs, func(chunk []any) error {
		in := placeholders(len(chunk))
		args := append(append([]any{kind}, chunk...), chunk...)
		_, err := b.tx.Exec(`DELETE FROM edge WHERE kind = ? AND reference_id IS NULL AND (
  src_symbol_id IN (SELECT id FROM symbol WHERE module_id IN (`+in+`)) OR
  dst_symbol_id IN (SELECT id FROM symbol WHERE module_id IN (`+in+`)))`, args...)
		return err
	})
}

// Target is the part of a symbol row reference resolution reads.
type Target struct {
	ID, FileID, ModuleID, RepoID int64
	StartLine                    int
	Kind, Name, Qualified        string
	Signature, Lang              string
	Value                        string // a constant's string value, "" when none
}

const targetSelect = `SELECT s.id, s.file_id, COALESCE(s.module_id,0), f.repo_id, s.start_line, s.kind, s.name,
  s.qualified_name, s.signature, COALESCE(f.lang,''), COALESCE(s.value,'') FROM symbol s JOIN file f ON f.id = s.file_id `

// Targets returns the symbols named one of names, or every symbol when
// names is nil, ordered by id.
func (b *Batch) Targets(names []string) ([]Target, error) {
	if names == nil {
		return queryTargets(b, `ORDER BY s.id`)
	}
	var out []Target
	err := inChunks(names, func(chunk []any) error {
		ts, err := queryTargets(b, `WHERE s.name IN (`+placeholders(len(chunk))+`) ORDER BY s.id`, chunk...)
		out = append(out, ts...)
		return err
	})
	return out, err
}

// TargetsOfKinds returns the symbols of the given kinds in files of a
// language, limited to the modules moduleIDs unless it is nil, ordered
// by id.
func (b *Batch) TargetsOfKinds(lang string, kinds []string, moduleIDs []int64) ([]Target, error) {
	where := `WHERE f.lang = ? AND s.kind IN (` + placeholders(len(kinds)) + `)`
	args := append([]any{lang}, anys(kinds)...)
	if moduleIDs == nil {
		return queryTargets(b, where+` ORDER BY s.id`, args...)
	}
	var out []Target
	err := inChunks(moduleIDs, func(chunk []any) error {
		ts, err := queryTargets(b, where+` AND s.module_id IN (`+placeholders(len(chunk))+`) ORDER BY s.id`,
			append(args, chunk...)...)
		out = append(out, ts...)
		return err
	})
	return out, err
}

// TargetsByQualified returns the symbols with a qualified name in the
// modules moduleIDs, ordered by id.
func (b *Batch) TargetsByQualified(qualified string, moduleIDs []int64) ([]Target, error) {
	var out []Target
	err := inChunks(moduleIDs, func(chunk []any) error {
		ts, err := queryTargets(b, `WHERE s.qualified_name = ? AND s.module_id IN (`+placeholders(len(chunk))+
			`) ORDER BY s.id`, append([]any{qualified}, chunk...)...)
		out = append(out, ts...)
		return err
	})
	return out, err
}

// TargetsNamedIn returns the symbols with a name in the modules
// moduleIDs, ordered by id.
func (b *Batch) TargetsNamedIn(name string, moduleIDs []int64) ([]Target, error) {
	var out []Target
	err := inChunks(moduleIDs, func(chunk []any) error {
		ts, err := queryTargets(b, `WHERE s.name = ? AND s.module_id IN (`+placeholders(len(chunk))+
			`) ORDER BY s.id`, append([]any{name}, chunk...)...)
		out = append(out, ts...)
		return err
	})
	return out, err
}

// TargetMembers returns the symbols of a kind declared directly inside
// the symbols with the qualified name container in the modules
// moduleIDs (the fields of a struct), ordered by id.
func (b *Batch) TargetMembers(container, kind string, moduleIDs []int64) ([]Target, error) {
	var out []Target
	err := inChunks(moduleIDs, func(chunk []any) error {
		ts, err := queryTargets(b, `WHERE s.kind = ? AND s.container_id IN (SELECT c.id FROM symbol c
  WHERE c.qualified_name = ? AND c.module_id IN (`+placeholders(len(chunk))+`)) ORDER BY s.id`,
			append([]any{kind, container}, chunk...)...)
		out = append(out, ts...)
		return err
	})
	return out, err
}

// TargetsOfKindsNamed returns the symbols of the given kinds in files of
// a language named one of names, ordered by id.
func (b *Batch) TargetsOfKindsNamed(lang string, kinds, names []string) ([]Target, error) {
	where := `WHERE f.lang = ? AND s.kind IN (` + placeholders(len(kinds)) + `)`
	args := append([]any{lang}, anys(kinds)...)
	var out []Target
	err := inChunks(names, func(chunk []any) error {
		ts, err := queryTargets(b, where+` AND s.name IN (`+placeholders(len(chunk))+`) ORDER BY s.id`,
			append(args, chunk...)...)
		out = append(out, ts...)
		return err
	})
	return out, err
}

// Embed is a type reference written directly inside an interface (an
// embedded interface or a type set term): Dst is the interface it
// resolved to, zero when it resolved to nothing, to several candidates
// or to another kind.
type Embed struct{ Src, Dst int64 }

// Embeds returns the type references of a language written directly
// inside interfaces, ordered by interface and reference.
func (b *Batch) Embeds(lang string) ([]Embed, error) {
	rows, err := b.tx.Query(`SELECT r.symbol_id, COALESCE((SELECT d.id FROM edge e JOIN symbol d ON d.id = e.dst_symbol_id
    WHERE e.reference_id = r.id AND d.kind = 'interface' LIMIT 1), 0)
  FROM reference r JOIN symbol s ON s.id = r.symbol_id JOIN file f ON f.id = r.file_id
  WHERE s.kind = 'interface' AND r.kind = 'type' AND f.lang = ? ORDER BY r.symbol_id, r.id`, lang)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Embed
	for rows.Next() {
		var e Embed
		if err := rows.Scan(&e.Src, &e.Dst); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Supertypes returns the symbols a symbol extends through resolved
// extends edges, ordered by edge id.
func (b *Batch) Supertypes(id int64) ([]Target, error) {
	return queryTargets(b, `JOIN edge e ON e.dst_symbol_id = s.id WHERE e.src_symbol_id = ? AND e.kind = 'extends'
  ORDER BY e.id`, id)
}

// ModuleEdges returns every module edge of the workspace.
func (b *Batch) ModuleEdges() ([]ModuleEdge, error) {
	rows, err := b.tx.Query(`SELECT src_module_id, dst_module_id, kind, confidence FROM module_edge`)
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

func queryTargets(b *Batch, where string, args ...any) ([]Target, error) {
	rows, err := b.tx.Query(targetSelect+where, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Target
	for rows.Next() {
		var t Target
		if err := rows.Scan(&t.ID, &t.FileID, &t.ModuleID, &t.RepoID, &t.StartLine, &t.Kind, &t.Name, &t.Qualified,
			&t.Signature, &t.Lang, &t.Value); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

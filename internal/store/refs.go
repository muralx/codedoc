package store

import (
	"database/sql"
	"errors"
)

// Reference is a reference row: one use of a name, kept so it can be
// resolved again when the symbols it may name change. SymbolID is the
// enclosing symbol, zero at file level. Candidates is the number of
// candidates of an ambiguous reference stored as unresolved because it
// had too many. Enclosing, EnclosingKind, EnclosingSig, ModuleID, RepoID
// and Lang are read only: the enclosing symbol's qualified name, kind,
// signature and module, and the file's repo and language.
type Reference struct {
	ID, FileID, SymbolID int64
	Kind, Name           string
	Qualifier            string
	// RecvType is the type of the variable the qualifier starts with,
	// "" when the extractor did not find it evident.
	RecvType    string
	Line, Arity int
	// Col is the 0-based byte column of the name, -1 when unknown.
	Col int
	// Added marks a row a SCIP merge added (see InsertReference).
	Added            bool
	Candidates       int
	Enclosing        string
	EnclosingKind    string
	EnclosingSig     string
	ModuleID, RepoID int64
	Lang             string
}

const referenceColumns = `r.id, r.file_id, COALESCE(r.symbol_id,0), r.kind, r.name, r.qualifier, r.line, r.col, r.added, r.arity,
  r.candidates, COALESCE(s.qualified_name,''), COALESCE(s.kind,''), COALESCE(s.signature,''),
  COALESCE(s.module_id,0), f.repo_id, COALESCE(f.lang,''), r.recv_type`

const referenceFrom = ` FROM reference r JOIN file f ON f.id = r.file_id LEFT JOIN symbol s ON s.id = r.symbol_id `

// ReplaceFileReferences deletes the references of a file, with the edges
// derived from them, and inserts refs.
func (b *Batch) ReplaceFileReferences(fileID int64, refs []Reference) error {
	if _, err := (cached{b}).Exec(`DELETE FROM reference WHERE file_id = ?`, fileID); err != nil {
		return err
	}
	for _, r := range refs {
		if _, err := (cached{b}).Exec(`INSERT INTO reference(file_id, symbol_id, kind, name, qualifier, line, col, arity, recv_type)
  VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`, fileID, nullID(r.SymbolID), r.Kind, r.Name, r.Qualifier, r.Line, r.Col, r.Arity,
			r.RecvType); err != nil {
			return err
		}
	}
	return nil
}

// References returns the references inside a symbol (file-level ones
// never become edges) and the config references, file-level ones
// included, of the files fileIDs plus those named one of names, ordered
// by id; with both nil, every such reference of the workspace.
func (b *Batch) References(fileIDs []int64, names []string) ([]Reference, error) {
	const where = `WHERE (r.symbol_id IS NOT NULL OR r.kind = 'config') `
	if fileIDs == nil && names == nil {
		return queryReferences(b.tx, where+`ORDER BY r.id`)
	}
	seen := map[int64]bool{}
	var out []Reference
	add := func(refs []Reference) {
		for _, r := range refs {
			if !seen[r.ID] {
				seen[r.ID] = true
				out = append(out, r)
			}
		}
	}
	err := inChunks(fileIDs, func(chunk []any) error {
		refs, err := queryReferences(b.tx, where+`AND r.file_id IN (`+placeholders(len(chunk))+`) ORDER BY r.id`, chunk...)
		add(refs)
		return err
	})
	if err != nil {
		return nil, err
	}
	err = inChunks(names, func(chunk []any) error {
		refs, err := queryReferences(b.tx, where+`AND r.name IN (`+placeholders(len(chunk))+`) ORDER BY r.id`, chunk...)
		add(refs)
		return err
	})
	return out, err
}

// ReferencesThrough returns the references inside a symbol whose
// qualifier is one of fields or ends with "." and one of them, or whose
// receiver type is a type path with a step through one of them
// (Options#client, see extract.FieldOf): the references that go through
// a field of that name, ordered by id.
func (b *Batch) ReferencesThrough(fields []string) ([]Reference, error) {
	var out []Reference
	for _, f := range fields {
		refs, err := queryReferences(b.tx, `WHERE r.symbol_id IS NOT NULL AND (r.qualifier = ? OR r.qualifier LIKE ?
  OR instr(r.recv_type, ?) > 0) ORDER BY r.id`, f, "%."+f, "#"+f)
		if err != nil {
			return nil, err
		}
		out = append(out, refs...)
	}
	return out, nil
}

// ReferencesKeyed returns the config references read through a constant
// whose value, set by SetReferenceConfigKey, is one of keys, ordered by
// id.
func (b *Batch) ReferencesKeyed(keys []string) ([]Reference, error) {
	var out []Reference
	err := inChunks(keys, func(chunk []any) error {
		refs, err := queryReferences(b.tx, `WHERE r.config_key IN (`+placeholders(len(chunk))+`) ORDER BY r.id`, chunk...)
		out = append(out, refs...)
		return err
	})
	return out, err
}

// FieldQualifierAt returns the qualifier of the field reference named
// name at a file position, "" when there is none: the receiver a config
// accessor's constant argument is reached through (pkg in pkg.KEY).
func (b *Batch) FieldQualifierAt(fileID int64, line, col int, name string) (string, error) {
	var q string
	err := (cached{b}).QueryRow(`SELECT qualifier FROM reference WHERE file_id = ? AND line = ? AND col = ? AND name = ?
  AND kind = 'field' ORDER BY id LIMIT 1`, fileID, line, col, name).Scan(&q)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return q, err
}

// SetReferenceConfigKey records the config key a config reference read
// through a constant names, the constant's value; "" clears it.
func (b *Batch) SetReferenceConfigKey(id int64, key string) error {
	_, err := (cached{b}).Exec(`UPDATE reference SET config_key = ? WHERE id = ?`, nullStr(key), id)
	return err
}

// SetReferenceCandidates records the candidate count of a reference left
// unresolved because it had too many candidates; 0 clears it.
func (b *Batch) SetReferenceCandidates(id int64, n int) error {
	_, err := (cached{b}).Exec(`UPDATE reference SET candidates = ? WHERE id = ?`, n, id)
	return err
}

// UnresolvedReferences returns the references of a file that have no
// edge and, for config references, no matched key (the
// reference_unresolved view), ordered by line.
func (s *Store) UnresolvedReferences(fileID int64) ([]Reference, error) {
	return queryReferences(s.db, `WHERE r.file_id = ? AND NOT EXISTS (SELECT 1 FROM edge e WHERE e.reference_id = r.id)
  AND NOT EXISTS (SELECT 1 FROM config_use u WHERE u.reference_id = r.id) ORDER BY r.line, r.id`, fileID)
}

func queryReferences(q rowsQuerier, where string, args ...any) ([]Reference, error) {
	rows, err := q.Query(`SELECT `+referenceColumns+referenceFrom+where, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Reference
	for rows.Next() {
		var r Reference
		if err := rows.Scan(&r.ID, &r.FileID, &r.SymbolID, &r.Kind, &r.Name, &r.Qualifier, &r.Line, &r.Col, &r.Added, &r.Arity,
			&r.Candidates, &r.Enclosing, &r.EnclosingKind, &r.EnclosingSig, &r.ModuleID, &r.RepoID, &r.Lang, &r.RecvType); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// inChunks calls fn with the items converted to query arguments, at most
// maxParams per call.
func inChunks[T any](items []T, fn func(chunk []any) error) error {
	for lo := 0; lo < len(items); lo += maxParams {
		if err := fn(anys(items[lo:min(lo+maxParams, len(items))])); err != nil {
			return err
		}
	}
	return nil
}

// RepoReferencesNamed returns the references of a repo's code inside a
// symbol whose name is one of names, ordered by path and line.
func (s *Store) RepoReferencesNamed(repoID int64, names []string) ([]Reference, error) {
	if len(names) == 0 {
		return nil, nil
	}
	return queryReferences(s.db, `WHERE f.repo_id = ? AND r.symbol_id IS NOT NULL AND r.name IN (`+
		placeholders(len(names))+`) ORDER BY f.path, r.line`, append([]any{repoID}, anys(names)...)...)
}

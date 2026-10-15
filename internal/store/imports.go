package store

import (
	"database/sql"
	"encoding/json"
)

// Import is an import row. ResolvedModuleID is zero when unresolved;
// RowID identifies the row; RepoID, Path and Lang come from the file row
// and are read only.
type Import struct {
	RowID, FileID    int64
	Raw, Alias       string
	Names            []string
	Line             int
	ResolvedModuleID int64
	RepoID           int64
	Path, Lang       string
}

const importColumns = `i.rowid, i.file_id, i.raw, COALESCE(i.alias,''), COALESCE(i.names,''), i.line,
  COALESCE(i.resolved_module_id,0), f.repo_id, f.path, COALESCE(f.lang,'')`

const importFrom = ` FROM import i JOIN file f ON f.id = i.file_id `

// ReplaceFileImports deletes the imports of a file and inserts imps,
// unresolved.
func (b *Batch) ReplaceFileImports(fileID int64, imps []Import) error {
	if _, err := (cached{b}).Exec(`DELETE FROM import WHERE file_id = ?`, fileID); err != nil {
		return err
	}
	for _, imp := range imps {
		var names any
		if imp.Names != nil {
			raw, err := json.Marshal(imp.Names)
			if err != nil {
				return err
			}
			names = string(raw)
		}
		if _, err := (cached{b}).Exec(`INSERT INTO import(file_id, raw, alias, names, line) VALUES(?, ?, ?, ?, ?)`,
			fileID, imp.Raw, nullStr(imp.Alias), names, imp.Line); err != nil {
			return err
		}
	}
	return nil
}

// SetImportModule records the module an import resolves to; 0 stores NULL.
// An import row that no longer exists is not an error: there is nothing
// left to resolve.
func (b *Batch) SetImportModule(rowID, moduleID int64) error {
	_, err := (cached{b}).Exec(`UPDATE import SET resolved_module_id = ? WHERE rowid = ?`, nullID(moduleID), rowID)
	return err
}

// Imports returns the imports of the given files, or of every file of the
// workspace when fileIDs is nil, ordered by file and line.
func (b *Batch) Imports(fileIDs []int64) ([]Import, error) {
	const order = ` ORDER BY i.file_id, i.line, i.rowid`
	if fileIDs == nil {
		return queryImports(b.tx, order)
	}
	var out []Import
	for lo := 0; lo < len(fileIDs); lo += maxParams {
		chunk := fileIDs[lo:min(lo+maxParams, len(fileIDs))]
		imps, err := queryImports(b.tx, `WHERE i.file_id IN (`+placeholders(len(chunk))+`)`+order, anys(chunk)...)
		if err != nil {
			return nil, err
		}
		out = append(out, imps...)
	}
	return out, nil
}

// ImportsInDir returns the imports of the files of a repo that sit
// directly in a directory ("." for the repo root), ordered by file and line.
func (b *Batch) ImportsInDir(repoID int64, dir string) ([]Import, error) {
	const order = ` ORDER BY i.file_id, i.line, i.rowid`
	if dir == "." {
		return queryImports(b.tx, `WHERE f.repo_id = ? AND instr(f.path, '/') = 0`+order, repoID)
	}
	// The range selects paths under dir/ ('0' sorts right after '/') through
	// the file(repo_id, path) index; instr keeps direct children only.
	return queryImports(b.tx, `WHERE f.repo_id = ? AND f.path > ? AND f.path < ?
  AND instr(substr(f.path, ?), '/') = 0`+order, repoID, dir+"/", dir+"0", len(dir)+2)
}

// FileImports returns the imports of a file in line order.
func (s *Store) FileImports(fileID int64) ([]Import, error) {
	return queryImports(s.db, `WHERE i.file_id = ? ORDER BY i.line, i.rowid`, fileID)
}

func queryImports(q rowsQuerier, where string, args ...any) ([]Import, error) {
	rows, err := q.Query(`SELECT `+importColumns+importFrom+where, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Import
	for rows.Next() {
		imp, err := scanImport(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, imp)
	}
	return out, rows.Err()
}

func scanImport(rows *sql.Rows) (Import, error) {
	var imp Import
	var names string
	err := rows.Scan(&imp.RowID, &imp.FileID, &imp.Raw, &imp.Alias, &names, &imp.Line, &imp.ResolvedModuleID,
		&imp.RepoID, &imp.Path, &imp.Lang)
	if err == nil && names != "" {
		err = json.Unmarshal([]byte(names), &imp.Names)
	}
	return imp, err
}

// ImportsAtPath returns the imports of the file of a repo at a path, in
// line order.
func (b *Batch) ImportsAtPath(repoID int64, path string) ([]Import, error) {
	return queryImports(b.tx, `WHERE f.repo_id = ? AND f.path = ? ORDER BY i.line, i.rowid`, repoID, path)
}

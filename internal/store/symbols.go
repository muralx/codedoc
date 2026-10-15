package store

import (
	"cmp"
	"database/sql"
	"slices"
	"strings"
)

// Symbol is a symbol row. ModuleID and ContainerID are zero when NULL;
// the Doc* string fields are empty and the Doc*Line fields zero when NULL.
type Symbol struct {
	ID, FileID, ModuleID int64
	Kind, Name           string
	Qualified, StableKey string
	ContainerID          int64
	// Container is the qualified name of the enclosing symbol in the same
	// file. It is input only: ReplaceFileSymbols resolves it to ContainerID.
	Container                string
	StartLine, EndLine       int
	StartByte, EndByte       int64
	SigStartLine, SigEndLine int
	Signature, Visibility    string
	IsTest                   bool
	BodyHash                 string
	DocText, DocHash         string
	DocBodyHash, DocState    string
	DocStartLine, DocEndLine int
	// Value is the string a constant is initialized with, "" when none
	// (see extract.Symbol.Value).
	Value      string
	RepoID     int64  // read only, from the file row
	Path, Lang string // read only, from the file row
}

const symbolColumns = `s.id, s.file_id, COALESCE(s.module_id,0), s.kind, s.name, s.qualified_name, s.stable_key,
  COALESCE(s.container_id,0), s.start_line, s.end_line, s.start_byte, s.end_byte, s.sig_start_line, s.sig_end_line,
  s.signature, s.visibility, s.is_test, s.body_hash, COALESCE(s.doc_text,''), COALESCE(s.doc_hash,''),
  COALESCE(s.doc_body_hash,''), s.doc_state, COALESCE(s.doc_start_line,0), COALESCE(s.doc_end_line,0),
  f.repo_id, f.path, COALESCE(f.lang,''), COALESCE(s.value,'')`

const symbolFrom = ` FROM symbol s JOIN file f ON f.id = s.file_id `

// rowsQuerier is the subset of *sql.DB and *sql.Tx the symbol readers use.
type rowsQuerier interface {
	querier
	Query(query string, args ...any) (*sql.Rows, error)
}

// ReplaceFileSymbols makes syms the symbols of a file in one transaction
// and returns their ids by stable key: rows whose stable key stays keep
// their id. moduleID 0 stores NULL.
func (s *Store) ReplaceFileSymbols(fileID, moduleID int64, syms []Symbol) (map[string]int64, error) {
	var ids map[string]int64
	err := s.Batch(func(b *Batch) error {
		var err error
		ids, err = b.ReplaceFileSymbols(fileID, moduleID, syms)
		return err
	})
	return ids, err
}

// ReplaceFileSymbols is Store.ReplaceFileSymbols inside the batch; see
// SyncFileSymbols, which it runs.
func (b *Batch) ReplaceFileSymbols(fileID, moduleID int64, syms []Symbol) (map[string]int64, error) {
	res, err := b.SyncFileSymbols(fileID, moduleID, syms)
	return res.IDs, err
}

// insertSymbol inserts a symbol row and returns its id. It reads the id
// back with LastInsertId rather than RETURNING, which costs a result
// row per insert (the FTS trigger's own insert does not change it).
func insertSymbol(q querier, fileID, moduleID int64, sy Symbol) (int64, error) {
	res, err := q.Exec(`INSERT INTO symbol(file_id, module_id, kind, name, qualified_name, stable_key, container_id,
    start_line, end_line, start_byte, end_byte, sig_start_line, sig_end_line, signature, visibility, is_test,
    body_hash, doc_text, doc_hash, doc_body_hash, doc_state, doc_start_line, doc_end_line, value)
  VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		fileID, nullID(moduleID), sy.Kind, sy.Name, sy.Qualified, sy.StableKey, nullID(sy.ContainerID),
		sy.StartLine, sy.EndLine, sy.StartByte, sy.EndByte, sy.SigStartLine, sy.SigEndLine, sy.Signature,
		sy.Visibility, sy.IsTest, sy.BodyHash, nullStr(sy.DocText), nullStr(sy.DocHash), nullStr(sy.DocBodyHash),
		defaultStr(sy.DocState, "missing"), nullID(int64(sy.DocStartLine)), nullID(int64(sy.DocEndLine)), nullStr(sy.Value))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// keyBasesPer is how many key bases one SymbolsByKeyBases statement
// takes. The number is fixed, the last base repeated to fill it, so that
// a batch prepares the statement once whatever the number of symbols in
// each file it writes.
const keyBasesPer = 64

// keyBasesWhere selects the symbols of keyBasesPer key ranges outside
// one file.
var keyBasesWhere = `WHERE s.id IN (` + strings.TrimSuffix(strings.Repeat(
	`SELECT id FROM symbol WHERE stable_key >= ? AND stable_key < ? UNION ALL `, keyBasesPer), ` UNION ALL `) +
	`) AND s.file_id <> ?`

// SymbolsByKeyBases returns the symbols of files other than excludeFile
// whose stable key starts with one of bases followed by nothing or by a
// character up to '#' (so base and base#hash, and a few other keys the
// caller filters out), each once and ordered by id, in few queries: one
// index range per base.
func (b *Batch) SymbolsByKeyBases(bases []string, excludeFile int64) ([]Symbol, error) {
	var out []Symbol
	for lo := 0; lo < len(bases); lo += keyBasesPer {
		chunk := bases[lo:min(lo+keyBasesPer, len(bases))]
		args := make([]any, 0, 2*keyBasesPer+1)
		for i := range keyBasesPer {
			base := chunk[min(i, len(chunk)-1)]
			args = append(args, base, base+"$")
		}
		syms, err := querySymbols(cached{b}, keyBasesWhere, append(args, excludeFile)...)
		if err != nil {
			return nil, err
		}
		out = append(out, syms...)
	}
	slices.SortFunc(out, func(x, y Symbol) int { return cmp.Compare(x.ID, y.ID) })
	return slices.CompactFunc(out, func(x, y Symbol) bool { return x.ID == y.ID }), nil
}

// SetStableKey rewrites the stable key of one symbol.
func (b *Batch) SetStableKey(id int64, key string) error {
	return requireOne(b.tx.Exec(`UPDATE symbol SET stable_key = ? WHERE id = ?`, key, id))
}

// SymbolByKey returns the symbol with a stable key.
func (s *Store) SymbolByKey(key string) (Symbol, error) {
	return querySymbol(s.db, `WHERE s.stable_key = ?`, key)
}

// SymbolByID returns the symbol with an id.
func (s *Store) SymbolByID(id int64) (Symbol, error) {
	return querySymbol(s.db, `WHERE s.id = ?`, id)
}

// SymbolsByName returns the symbols named name, optionally limited to a
// kind ("" for any) and a repo (0 for any), ordered by repo, path, line.
func (s *Store) SymbolsByName(name, kind string, repoID int64) ([]Symbol, error) {
	return querySymbols(s.db, `WHERE s.name = ?`+kindRepoFilter+symbolOrder, name, kind, kind, repoID, repoID)
}

// SymbolsByGlob returns the symbols whose name matches a GLOB pattern, or
// whose qualified name does when the pattern contains a dot or a slash
// (also after a TypeScript file prefix, "path/"), filtered like
// SymbolsByName.
func (s *Store) SymbolsByGlob(pattern, kind string, repoID int64) ([]Symbol, error) {
	where, args := `WHERE s.name GLOB ?`, []any{pattern}
	if strings.ContainsAny(pattern, "./") {
		where, args = `WHERE (s.qualified_name GLOB ? OR s.qualified_name GLOB '*/' || ?)`, []any{pattern, pattern}
	}
	args = append(args, kind, kind, repoID, repoID)
	return querySymbols(s.db, where+kindRepoFilter+symbolOrder, args...)
}

const kindRepoFilter = ` AND (? = '' OR s.kind = ?) AND (? = 0 OR f.repo_id = ?)`

const symbolOrder = ` ORDER BY f.repo_id, f.path, s.start_line, s.id`

// SymbolsInFile returns the symbols of a file ordered by start line.
func (s *Store) SymbolsInFile(fileID int64) ([]Symbol, error) {
	return querySymbols(s.db, `WHERE s.file_id = ? ORDER BY s.start_line, s.id`, fileID)
}

// SymbolsInModule returns the symbols of a module ordered by path and line.
func (s *Store) SymbolsInModule(moduleID int64) ([]Symbol, error) {
	return querySymbols(s.db, `WHERE s.module_id = ?`+symbolOrder, moduleID)
}

// SymbolByLocation returns the innermost symbol of a file whose span
// contains line.
func (s *Store) SymbolByLocation(fileID int64, line int) (Symbol, error) {
	return querySymbol(s.db, `WHERE s.file_id = ? AND s.start_line <= ? AND s.end_line >= ?
  ORDER BY s.end_line - s.start_line, s.start_line DESC LIMIT 1`, fileID, line, line)
}

func querySymbol(q querier, where string, args ...any) (Symbol, error) {
	sy, err := scanSymbol(q.QueryRow(`SELECT `+symbolColumns+symbolFrom+where, args...))
	return sy, notFound(err)
}

func querySymbols(q rowsQuerier, where string, args ...any) ([]Symbol, error) {
	rows, err := q.Query(`SELECT `+symbolColumns+symbolFrom+where, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Symbol
	for rows.Next() {
		sy, err := scanSymbol(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sy)
	}
	return out, rows.Err()
}

func scanSymbol(sc scanner) (Symbol, error) {
	var s Symbol
	err := sc.Scan(&s.ID, &s.FileID, &s.ModuleID, &s.Kind, &s.Name, &s.Qualified, &s.StableKey, &s.ContainerID,
		&s.StartLine, &s.EndLine, &s.StartByte, &s.EndByte, &s.SigStartLine, &s.SigEndLine, &s.Signature,
		&s.Visibility, &s.IsTest, &s.BodyHash, &s.DocText, &s.DocHash, &s.DocBodyHash, &s.DocState,
		&s.DocStartLine, &s.DocEndLine, &s.RepoID, &s.Path, &s.Lang, &s.Value)
	return s, err
}

func nullID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func defaultStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

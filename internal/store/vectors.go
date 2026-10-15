package store

import (
	"encoding/binary"
	"math"
	"strconv"
	"strings"
)

// Vectors are stored as little-endian float32 BLOBs: one per passage of
// the embedded text of a symbol (symbol_vec, see ReplaceSymbolVecs) and
// one per documented module (module_vec), next to the hash of the text
// they were computed from.

// EncodeVec returns the little-endian float32 BLOB of v.
func EncodeVec(v []float32) []byte {
	out := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(out[4*i:], math.Float32bits(f))
	}
	return out
}

// DecodeVec returns the float32 vector of a BLOB written by EncodeVec.
func DecodeVec(b []byte) []float32 {
	out := make([]float32, len(b)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return out
}

// symbolVecHashSQL is the text hash stored with the vectors of symbol s:
// the passage scheme (p1), its doc hash and its body hash. The body hash
// covers the signature, and a symbol whose path or container changes is
// a new row, so the hash identifies the embedded text (spec 7.7) and
// lets staleness be decided in SQL without reading source files.
const symbolVecHashSQL = `'p1:' || COALESCE(s.doc_hash,'') || ':' || s.body_hash`

// unparkedSQL selects the symbols of symbol s a sync in progress has not
// parked.
const unparkedSQL = `substr(s.stable_key, 1, 1) <> char(1)`

// documentedSQL selects the symbols of symbol s embedded from their
// documentation: the documented ones, and every document and section,
// whose text is its own documentation (documents spec section 2).
const documentedSQL = `(COALESCE(s.doc_text,'') <> '' OR NOT ` + codeKindSQL + `)`

// bodySQL selects the undocumented symbols of symbol s of file f whose
// source is worth a vector: not those of generated files, not tests,
// and not the trivial ones, a field, a constant or any declaration of
// one line (an accessor, an enum member, a property). Those are found by
// name, crowd the results of a search and double what there is to embed.
const bodySQL = `f.generated = 0 AND s.is_test = 0 AND s.kind NOT IN ('field', 'const') AND s.end_line > s.start_line`

// embeddedSQL selects the unparked symbols of symbol s of file f that
// are embedded: the documented ones and, with bodies, the undocumented
// ones bodySQL selects, embedded from their source.
func embeddedSQL(bodies bool) string {
	if bodies {
		return `(` + documentedSQL + ` OR (` + bodySQL + `)) AND ` + unparkedSQL
	}
	return documentedSQL + ` AND ` + unparkedSQL
}

// ownEndSQL is the last line of the own text of document or section s
// when a section starts inside it (index.documentTexts cuts the text the
// section text index holds there), NULL otherwise.
const ownEndSQL = `(SELECT MIN(m.start_line) - 1 FROM symbol m WHERE NOT ` + codeKindSQL + ` AND m.file_id = s.file_id
    AND m.id <> s.id AND m.start_line >= s.start_line + (s.kind = 'section') AND m.start_line <= s.end_line)`

// moduleDocumentedSQL selects the documented modules of module m.
const moduleDocumentedSQL = `COALESCE(m.doc_text,'') <> ''`

// VecScope selects the rows an embedding pass or count covers: the
// symbols and modules of RepoID (0 for all repos), with Touched only the
// symbols of FileIDs. Modules are few, so they are never narrowed further.
// Bodies adds the undocumented symbols, embedded from their source: set
// for a workspace index, not for the dependency database, which keeps no
// source to read.
type VecScope struct {
	RepoID  int64
	Touched bool
	FileIDs []int64
	Bodies  bool
}

// StaleSymbol is an embedded symbol whose vectors are missing or stale,
// with what its embedding texts are composed from.
type StaleSymbol struct {
	ID, RepoID, FileID       int64
	Kind, Qualified          string
	Path, Container          string
	Signature, Doc           string
	StartLine, EndLine       int
	SigStartLine, SigEndLine int
	// DocStartLine and DocEndLine are zero when the lines of the doc are
	// not known.
	DocStartLine, DocEndLine int
	// OwnEndLine is, for a document or section, the last line before the
	// next symbol of the file that starts inside it, where its own text
	// ends; EndLine when there is none and for code.
	OwnEndLine int
	// Members are the names of the symbols the symbol contains, in file
	// order, separated by spaces.
	Members              string
	FileHash, VectorHash string
}

// StaleSymbolVecs returns the embedded symbols of the scope without a
// vector, with vectors of another text hash, or (dims above 0) with
// vectors of another size.
func (s *Store) StaleSymbolVecs(sc VecScope, dims int) ([]StaleSymbol, error) {
	where, args := symbolScope(sc)
	q := `SELECT s.id, f.repo_id, s.file_id, s.kind, s.qualified_name, f.path, COALESCE(c.qualified_name,''),
  s.signature, COALESCE(s.doc_text,''), s.start_line, s.end_line, s.sig_start_line, s.sig_end_line,
  COALESCE(s.doc_start_line,0), COALESCE(s.doc_end_line,0),
  COALESCE(` + ownEndSQL + `, s.end_line),
  COALESCE((SELECT group_concat(name, ' ') FROM (SELECT m.name FROM symbol m WHERE m.container_id = s.id
    ORDER BY m.start_line, m.id)), ''),
  f.hash, ` + symbolVecHashSQL + `
  FROM symbol s JOIN file f ON f.id = s.file_id LEFT JOIN symbol c ON c.id = s.container_id
  LEFT JOIN symbol_vec v ON v.symbol_id = s.id AND v.ord = 0
  WHERE ` + embeddedSQL(sc.Bodies) + where + ` AND (v.symbol_id IS NULL OR v.text_hash <> ` + symbolVecHashSQL + `
  OR (? > 0 AND length(v.vec) <> 4 * ?)) ORDER BY s.id`
	rows, err := s.db.Query(q, append(args, dims, dims)...)
	if err != nil {
		return nil, err
	}
	var out []StaleSymbol
	for rows.Next() {
		var st StaleSymbol
		if err := rows.Scan(&st.ID, &st.RepoID, &st.FileID, &st.Kind, &st.Qualified, &st.Path, &st.Container,
			&st.Signature, &st.Doc, &st.StartLine, &st.EndLine, &st.SigStartLine, &st.SigEndLine,
			&st.DocStartLine, &st.DocEndLine, &st.OwnEndLine, &st.Members, &st.FileHash, &st.VectorHash); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, st)
	}
	return out, closeRows(rows)
}

// symbolScope is the condition (starting with AND) and arguments that
// limit symbol s of file f to a scope.
func symbolScope(sc VecScope) (string, []any) {
	var where strings.Builder
	var args []any
	if sc.RepoID != 0 {
		where.WriteString(` AND f.repo_id = ?`)
		args = append(args, sc.RepoID)
	}
	if sc.Touched {
		where.WriteString(` AND s.file_id IN (SELECT value FROM json_each(?))`)
		args = append(args, idList(sc.FileIDs))
	}
	return where.String(), args
}

// idList renders ids as a JSON array for json_each, which binds any
// number of ids as one parameter.
func idList(ids []int64) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, id := range ids {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatInt(id, 10))
	}
	b.WriteByte(']')
	return b.String()
}

// SymbolVecCounts counts the embedded symbols of a scope (see VecScope;
// Touched is ignored) and those whose vectors are up to date (dims above
// 0 also checks their size). It counts symbols, not passages: a symbol
// with several passage vectors counts once.
func (s *Store) SymbolVecCounts(sc VecScope, dims int) (embedded, total int, err error) {
	where, args := symbolScope(VecScope{RepoID: sc.RepoID})
	err = s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(v.symbol_id IS NOT NULL AND v.text_hash = `+symbolVecHashSQL+`
  AND (? = 0 OR length(v.vec) = 4 * ?)), 0)
  FROM symbol s JOIN file f ON f.id = s.file_id LEFT JOIN symbol_vec v ON v.symbol_id = s.id AND v.ord = 0
  WHERE `+embeddedSQL(sc.Bodies)+where, append([]any{dims, dims}, args...)...).Scan(&total, &embedded)
	return embedded, total, err
}

// PassageCount counts the passage vectors of the symbols of a repo (0
// for all).
func (s *Store) PassageCount(repoID int64) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM symbol_vec v JOIN symbol s ON s.id = v.symbol_id
  JOIN file f ON f.id = s.file_id WHERE ? = 0 OR f.repo_id = ?`, repoID, repoID).Scan(&n)
	return n, err
}

// ModuleVec is a documented module with the text hash and size of its
// stored vector; Hash is empty and Dims zero without one.
type ModuleVec struct {
	Module
	Hash string
	Dims int
}

// ModuleVecs returns the documented modules of a scope with the state of
// their vectors.
func (s *Store) ModuleVecs(sc VecScope) ([]ModuleVec, error) {
	where, args := moduleDocumentedSQL, []any{}
	if sc.RepoID != 0 {
		where += ` AND m.repo_id = ?`
		args = append(args, sc.RepoID)
	}
	rows, err := s.db.Query(`SELECT m.id, m.repo_id, m.path, m.lang, m.kind, m.identity, COALESCE(m.parent_id,0),
  COALESCE(m.doc_text,''), COALESCE(m.doc_source,''), m.doc_state, COALESCE(v.text_hash,''), COALESCE(length(v.vec),0) / 4
  FROM module m LEFT JOIN module_vec v ON v.module_id = m.id WHERE `+where+` ORDER BY m.id`, args...)
	if err != nil {
		return nil, err
	}
	var out []ModuleVec
	for rows.Next() {
		var m ModuleVec
		if err := rows.Scan(&m.ID, &m.RepoID, &m.Path, &m.Lang, &m.Kind, &m.Identity, &m.ParentID, &m.DocText,
			&m.DocSource, &m.DocState, &m.Hash, &m.Dims); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, m)
	}
	return out, closeRows(rows)
}

// unembeddedVecs select the symbol and module ids of a repo (bound
// twice, 0 for all) whose vectors outlived what they were embedded for:
// an undocumented symbol bodySQL leaves out (its file became generated,
// it shrank to one line, or an earlier build embedded it), a module that
// lost its doc.
const (
	unembeddedSymbolVecs = `SELECT v.symbol_id FROM symbol_vec v JOIN symbol s ON s.id = v.symbol_id
  JOIN file f ON f.id = s.file_id WHERE NOT ` + documentedSQL + ` AND NOT (` + bodySQL + `)
  AND ` + unparkedSQL + ` AND (? = 0 OR f.repo_id = ?)`
	unembeddedModuleVecs = `SELECT v.module_id FROM module_vec v JOIN module m ON m.id = v.module_id
  WHERE NOT (` + moduleDocumentedSQL + `) AND (? = 0 OR m.repo_id = ?)`
)

// DeleteUnembeddedVecs deletes the vectors of the symbols and modules of
// a workspace repo (0 for all) that are not embedded any more, and
// returns how many symbols and modules lost theirs. Parked symbols keep
// their vectors: a sync in progress parks them until it writes their
// file again. Without such vectors nothing is written.
func (s *Store) DeleteUnembeddedVecs(repoID int64) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT (SELECT COUNT(DISTINCT symbol_id) FROM (`+unembeddedSymbolVecs+`))
  + (SELECT COUNT(*) FROM (`+unembeddedModuleVecs+`))`, repoID, repoID, repoID, repoID).Scan(&n)
	if err != nil || n == 0 {
		return 0, err
	}
	return n, s.Batch(func(b *Batch) error {
		for _, q := range []string{
			`DELETE FROM symbol_vec WHERE symbol_id IN (` + unembeddedSymbolVecs + `)`,
			`DELETE FROM module_vec WHERE module_id IN (` + unembeddedModuleVecs + `)`,
		} {
			if _, err := b.tx.Exec(q, repoID, repoID); err != nil {
				return err
			}
		}
		return nil
	})
}

// PassageVec is the vector of one passage of a symbol's embedded text.
// Start and End are the lines the passage covers, counted from the first
// line of the symbol's doc with FromDoc, else of the symbol.
type PassageVec struct {
	FromDoc    bool
	Start, End int
	Vec        []float32
}

// ReplaceSymbolVecs makes passages, in order, the vectors of a symbol,
// computed from the text of a hash; a symbol deleted since its text was
// read is skipped.
func (b *Batch) ReplaceSymbolVecs(id int64, hash string, passages []PassageVec) error {
	if _, err := (cached{b}).Exec(`DELETE FROM symbol_vec WHERE symbol_id = ?`, id); err != nil {
		return err
	}
	for ord, p := range passages {
		_, err := cached{b}.Exec(`INSERT INTO symbol_vec(symbol_id, ord, from_doc, start_line, end_line, text_hash, vec)
  SELECT ?, ?, ?, ?, ?, ?, ? WHERE EXISTS (SELECT 1 FROM symbol WHERE id = ?)`,
			id, ord, p.FromDoc, p.Start, p.End, hash, EncodeVec(p.Vec), id)
		if err != nil {
			return err
		}
	}
	return nil
}

// UpsertModuleVec stores the vector of a module; a module deleted since
// its text was read is skipped.
func (b *Batch) UpsertModuleVec(id int64, hash string, vec []float32) error {
	_, err := cached{b}.Exec(`INSERT INTO module_vec(module_id, text_hash, vec)
  SELECT ?, ?, ? WHERE EXISTS (SELECT 1 FROM module WHERE id = ?)
  ON CONFLICT(module_id) DO UPDATE SET text_hash = excluded.text_hash, vec = excluded.vec`, id, hash, EncodeVec(vec), id)
	return err
}

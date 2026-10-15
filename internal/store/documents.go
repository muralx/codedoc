package store

// codeKindSQL selects the symbols of symbol s that are code: every kind
// but document and section, which take no part in doc coverage, gaps or
// doc checks (documents spec section 2).
const codeKindSQL = `s.kind NOT IN ('document', 'section')`

// LinkRefKind is the reference kind of the references edge a relative
// link between documents makes; LinkConfidence its confidence.
const (
	LinkRefKind    = "link"
	LinkConfidence = "path"
)

// ReplaceSectionTexts makes texts, by symbol id, the searchable own
// text of the document and section symbols of a file.
func (b *Batch) ReplaceSectionTexts(fileID int64, texts map[int64]string) error {
	if _, err := (cached{b}).Exec(`DELETE FROM section_fts WHERE rowid IN (SELECT id FROM symbol WHERE file_id = ?)`,
		fileID); err != nil {
		return err
	}
	for id, text := range texts {
		if text == "" {
			continue
		}
		if _, err := (cached{b}).Exec(`INSERT INTO section_fts(rowid, text) VALUES(?, ?)`, id, text); err != nil {
			return err
		}
	}
	return nil
}

// SectionMatches returns the ids of the document and section symbols
// whose own text matches an FTS5 query, best BM25 first, at most k.
func (s *Store) SectionMatches(match string, f SearchFilter, k int) ([]int64, error) {
	where, args := symbolFilter(f)
	return s.matchIDs(`SELECT s.id FROM section_fts JOIN symbol s ON s.id = section_fts.rowid
  JOIN file f ON f.id = s.file_id
  WHERE section_fts MATCH ? AND substr(s.stable_key, 1, 1) <> char(1)`+where+`
  ORDER BY bm25(section_fts), s.id LIMIT ?`, match, args, k)
}

// SectionTexts returns the own text of the document and section symbols
// ids, by id; a symbol without text has no entry.
func (s *Store) SectionTexts(ids []int64) (map[int64]string, error) {
	out := map[int64]string{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(`SELECT rowid, text FROM section_fts WHERE rowid IN (SELECT value FROM json_each(?))`,
		idList(ids))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var text string
		if err := rows.Scan(&id, &text); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out[id] = text
	}
	return out, closeRows(rows)
}

// DocLink is a relative link of a document: SymbolID is the section (or
// document) it is written in, Target the repo-relative path it points
// at and Anchor the fragment after '#'. TargetFileID is read only: the
// indexed file at Target.
type DocLink struct {
	FileID, SymbolID int64
	Target, Anchor   string
	Line             int
	TargetFileID     int64
}

// ReplaceDocLinks makes links the links of a file.
func (b *Batch) ReplaceDocLinks(fileID int64, links []DocLink) error {
	if _, err := (cached{b}).Exec(`DELETE FROM doc_link WHERE file_id = ?`, fileID); err != nil {
		return err
	}
	for _, l := range links {
		if _, err := (cached{b}).Exec(`INSERT INTO doc_link(file_id, symbol_id, target, anchor, line)
  VALUES(?, ?, ?, ?, ?)`, fileID, l.SymbolID, l.Target, l.Anchor, l.Line); err != nil {
			return err
		}
	}
	return nil
}

// DocLinks returns the links of a repo's documents whose target is an
// indexed file of the repo, ordered by file and line.
func (b *Batch) DocLinks(repoID int64) ([]DocLink, error) {
	rows, err := b.tx.Query(`SELECT l.file_id, l.symbol_id, l.target, l.anchor, l.line, t.id
  FROM doc_link l JOIN file f ON f.id = l.file_id JOIN file t ON t.repo_id = f.repo_id AND t.path = l.target
  WHERE f.repo_id = ? ORDER BY l.file_id, l.line, l.rowid`, repoID)
	if err != nil {
		return nil, err
	}
	var out []DocLink
	for rows.Next() {
		var l DocLink
		if err := rows.Scan(&l.FileID, &l.SymbolID, &l.Target, &l.Anchor, &l.Line, &l.TargetFileID); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, l)
	}
	return out, closeRows(rows)
}

// DeleteLinkEdges deletes the edges derived from the links of a repo's
// documents.
func (b *Batch) DeleteLinkEdges(repoID int64) error {
	_, err := b.tx.Exec(`DELETE FROM edge WHERE ref_kind = ? AND reference_id IS NULL
  AND file_id IN (SELECT id FROM file WHERE repo_id = ?)`, LinkRefKind, repoID)
	return err
}

// DocSymbols returns the document and section symbols of a repo by file
// id, each file's in line order.
func (b *Batch) DocSymbols(repoID int64) (map[int64][]Symbol, error) {
	syms, err := querySymbols(b.tx, `WHERE f.repo_id = ? AND NOT `+codeKindSQL+`
  AND substr(s.stable_key, 1, 1) <> char(1) ORDER BY s.file_id, s.start_line, s.id`, repoID)
	out := map[int64][]Symbol{}
	for _, sy := range syms {
		out[sy.FileID] = append(out[sy.FileID], sy)
	}
	return out, err
}

// SymbolsLike returns the symbols of a kind whose name or signature
// contains text, ignoring ASCII case, optionally limited to a repo (0
// for any), ordered by repo, path and line: how find matches headings.
func (s *Store) SymbolsLike(text, kind string, repoID int64) ([]Symbol, error) {
	pattern := "%" + escapeLike(text) + "%"
	return querySymbols(s.db, `WHERE (s.name LIKE ? ESCAPE '\' OR s.signature LIKE ? ESCAPE '\')
  AND substr(s.stable_key, 1, 1) <> char(1)`+kindRepoFilter+symbolOrder, pattern, pattern, kind, kind, repoID, repoID)
}

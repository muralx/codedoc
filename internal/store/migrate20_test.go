package store

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestSchemaNineteenKeepsItsVectorsAsPassages opens a database at schema
// 19 with one vector per symbol and checks that migration 20 turns each
// into passage 0 of its symbol: current, covering the doc or the
// section's own lines, where the text embedded is the same under the
// passage scheme (a short doc, a short section), and still scanned but
// stale where it is not (a long doc, a long section), so the next
// embedding pass replaces exactly those.
func TestSchemaNineteenKeepsItsVectorsAsPassages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ws.db")
	all := migrations
	migrations = all[:19]
	s, err := Open(path)
	migrations = all
	if err != nil {
		t.Fatal(err)
	}
	seedSchemaNineteenVectors(t, s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s, err = Open(path); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if v, err := s.SchemaVersion(); err != nil || v != len(migrations) || v < 20 {
		t.Fatalf("schema %d: %v", v, err)
	}
	want := map[int64]vecRow{
		1: {1, 0, 1, "p1:d1:b1", 8},  // short doc: current, the doc's two lines
		2: {0, 0, 9, "d2:b2", 8},     // long doc: stale, the symbol's lines
		3: {0, 0, 3, "p1::b3", 8},    // short section: current, heading to the line before its sub-section
		4: {0, 0, 10, ":b4", 8},      // long section: stale
		5: {0, 0, 4, "p1:d5:b5", 8},  // short doc of unknown lines: current, the symbol's lines
		6: {0, 0, 0, "p1::b6", 8},    // section without text: current
		7: {0, 0, 2, "stale:old", 8}, // a vector an undocumented symbol kept: stale
	}
	for id, w := range want {
		var got vecRow
		err := s.db.QueryRow(`SELECT from_doc, start_line, end_line, text_hash, length(vec) FROM symbol_vec
  WHERE symbol_id = ? AND ord = 0`, id).Scan(&got.fromDoc, &got.start, &got.end, &got.hash, &got.size)
		if err != nil || got != w {
			t.Errorf("symbol %d: %+v, want %+v: %v", id, got, w, err)
		}
	}
	checkMigratedVectorsServe(t, s)
}

// vecRow is a row of symbol_vec without its vector.
type vecRow struct {
	fromDoc, start, end int
	hash                string
	size                int
}

// checkMigratedVectorsServe checks that the migrated vectors that are
// not current are exactly what the next pass embeds, that all of them
// are scanned meanwhile, and that the old table is gone.
func checkMigratedVectorsServe(t *testing.T, s *Store) {
	t.Helper()
	stale, err := s.StaleSymbolVecs(VecScope{Bodies: true}, 2)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, st := range stale {
		ids = append(ids, st.ID)
	}
	if !slices.Equal(ids, []int64{2, 4, 7}) {
		t.Fatalf("stale after the migration: %v", ids)
	}
	got, err := s.NearestSymbols([]float32{1, 0}, SearchFilter{}, 10)
	if err != nil || len(got) != 7 {
		t.Fatalf("every migrated vector is scanned: %+v %v", got, err)
	}
	for _, g := range got {
		if g.ID == 1 && !slices.Equal(g.Passages, []ScoredPassage{{StartLine: 8, EndLine: 9, Score: 1}}) {
			t.Fatalf("the short doc's passage covers the doc's file lines: %+v", g.Passages)
		}
	}
	if tables := queryStrings(t, s, `SELECT name FROM sqlite_master WHERE name LIKE 'symbol_vec%' AND type = 'table'`); len(tables) != 1 {
		t.Fatalf("tables %v", tables)
	}
}

// seedSchemaNineteenVectors writes symbols of each embedded shape with
// the single vector schema 19 kept for each.
func seedSchemaNineteenVectors(t *testing.T, s *Store) {
	t.Helper()
	long := strings.Repeat("word ", 300)
	mustExec(t, s, `INSERT INTO workspace(id, name, created_at) VALUES(1, 'w', 0)`)
	mustExec(t, s, `INSERT INTO repo(id, workspace_id, name, path) VALUES(1, 1, 'a', '/a')`)
	mustExec(t, s, `INSERT INTO file(id, repo_id, path, size, mtime, hash) VALUES(1, 1, 'x.go', 1, 1, 'h')`)
	insert := `INSERT INTO symbol(id, file_id, kind, name, qualified_name, stable_key, container_id, start_line, end_line,
		start_byte, end_byte, sig_start_line, sig_end_line, signature, visibility, body_hash, doc_text, doc_hash,
		doc_start_line, doc_end_line) VALUES(?, 1, ?, 'n', 'n', ?, ?, ?, ?, 0, 1, ?, ?, 'sig', 'public', ?, ?, ?, ?, ?)`
	mustExec(t, s, insert, 1, "function", "k1", nil, 10, 12, 10, 10, "b1", "Short doc.", "d1", 8, 9)
	mustExec(t, s, insert, 2, "function", "k2", nil, 40, 49, 40, 40, "b2", long, "d2", 20, 39)
	mustExec(t, s, insert, 3, "section", "k3", nil, 60, 80, 60, 60, "b3", nil, nil, nil, nil)
	mustExec(t, s, insert, 4, "section", "k4", 3, 64, 74, 64, 64, "b4", nil, nil, nil, nil)
	mustExec(t, s, insert, 5, "function", "k5", nil, 90, 94, 90, 90, "b5", "Doc set elsewhere.", "d5", nil, nil)
	mustExec(t, s, insert, 6, "section", "k6", 3, 75, 75, 75, 75, "b6", nil, nil, nil, nil)
	mustExec(t, s, insert, 7, "function", "k7", nil, 96, 98, 96, 96, "b7", nil, nil, nil, nil)
	mustExec(t, s, `INSERT INTO section_fts(rowid, text) VALUES(3, 'A short section.')`)
	mustExec(t, s, `INSERT INTO section_fts(rowid, text) VALUES(4, ?)`, long)
	for id, hash := range map[int]string{1: "d1:b1", 2: "d2:b2", 3: ":b3", 4: ":b4", 5: "d5:b5", 6: ":b6", 7: "stale:old"} {
		mustExec(t, s, `INSERT INTO symbol_vec(symbol_id, text_hash, vec) VALUES(?, ?, ?)`, id, hash,
			EncodeVec([]float32{1, 0}))
	}
}

package store

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryForeignKeyColumnLeadsAnIndex checks that the referencing
// column of every foreign key is the first column of an index over all
// rows or of the primary key: deleting the row it refers to (a cascade,
// or the check that nothing refers to it) then looks its children up
// instead of scanning their table.
func TestEveryForeignKeyColumnLeadsAnIndex(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "ws.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	tables := queryStrings(t, s, `SELECT name FROM sqlite_master WHERE type = 'table'`)
	checked := 0
	for _, table := range tables {
		leading := map[string]bool{}
		for _, col := range queryStrings(t, s, `SELECT name FROM pragma_table_info(?) WHERE pk = 1`, table) {
			leading[col] = true
		}
		for _, col := range queryStrings(t, s, `SELECT i.name FROM pragma_index_list(?) l, pragma_index_info(l.name) i
  WHERE l.partial = 0 AND i.seqno = 0`, table) {
			leading[col] = true
		}
		for _, col := range queryStrings(t, s, `SELECT "from" FROM pragma_foreign_key_list(?)`, table) {
			checked++
			if !leading[col] {
				t.Errorf("%s.%s refers to another table and leads no index", table, col)
			}
		}
	}
	if checked < 30 {
		t.Fatalf("only %d foreign keys found", checked)
	}
}

// TestSchemaSeventeenGainsTheLookupIndexes opens a database at schema 17
// and checks that the next open adds the indexes of migration 18 and
// that the lookups they are for use them.
func TestSchemaSeventeenGainsTheLookupIndexes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ws.db")
	all := migrations
	migrations = all[:17]
	s, err := Open(path)
	migrations = all
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, s, `INSERT INTO dep_user(package, workspace_db, seen_at) VALUES('npm/x@1', '/data/ws.db', 1)`)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s, err = Open(path); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	for query, index := range map[string]string{
		`SELECT src_module_id FROM module_edge WHERE dst_module_id = 1`: "module_edge_dst",
		`UPDATE module SET parent_id = NULL WHERE parent_id = 1`:        "module_parent",
		`DELETE FROM dep_user WHERE workspace_db = 'x'`:                 "dep_user_workspace",
	} {
		plan := queryPlan(t, s, query)
		if !strings.Contains(plan, index) {
			t.Errorf("%s\n  does not use %s: %s", query, index, plan)
		}
	}
	if users, err := s.DepUsers("/data/ws.db"); err != nil || len(users) != 1 {
		t.Fatalf("%+v %v", users, err)
	}
}

// queryStrings returns the first column of the rows of a query.
func queryStrings(t *testing.T, s *Store, q string, args ...any) []string {
	t.Helper()
	rows, err := s.db.Query(q, args...)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	out, err := scanStrings(rows)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return out
}

// queryPlan returns the steps of SQLite's plan for a statement.
func queryPlan(t *testing.T, s *Store, q string) string {
	t.Helper()
	rows, err := s.db.Query(`EXPLAIN QUERY PLAN ` + q)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	defer func() { _ = rows.Close() }()
	var steps []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		steps = append(steps, detail)
	}
	return strings.Join(steps, "; ")
}

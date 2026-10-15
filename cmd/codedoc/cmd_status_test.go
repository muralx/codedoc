package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStatusFreshnessMatchesQueries checks status calls a repo fresh by
// the query headers' definition: an indexed uncommitted edit is fresh,
// files of an older extractor version are not, and parse errors show.
func TestStatusFreshnessMatchesQueries(t *testing.T) {
	cfgDir, goLib := setupWorkspace(t)
	if err := os.WriteFile(filepath.Join(goLib, "store", "broken.go"), []byte("package store\n\nfunc (\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runCLI(t, "index", "--workspace", "fx"); err != nil {
		t.Fatal(err)
	}
	out, err := runCLI(t, "status", "--workspace", "fx", "--repo", "go-lib")
	if err != nil || !strings.HasPrefix(out, "go-lib@0b29eeb dirty\n") || strings.Contains(out, "parse-errors:0") {
		t.Fatalf("an indexed dirty tree is fresh and shows its parse errors: %v\n%s", err, out)
	}
	db, err := sql.Open("sqlite3", filepath.Join(cfgDir, "data", "fx.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE file SET extracted_version = '0' WHERE repo_id = (SELECT id FROM repo WHERE name = 'go-lib')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	out, err = runCLI(t, "status", "--workspace", "fx", "--repo", "go-lib")
	if err != nil || !strings.Contains(out, "fresh:no\n") || !strings.Contains(out, "(index behind: extractor updated, 9 files to re-extract; the next query or codedoc index brings it up to date)") {
		t.Fatalf("an outdated extractor is not fresh: %v\n%s", err, out)
	}
}

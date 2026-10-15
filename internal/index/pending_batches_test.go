package index_test

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3" // sqlite driver for the edge dump

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/store"
	"github.com/muralx/codedoc/internal/testutil"
)

// shapesRepo is a Go repo whose struct S implements I through a method
// declared in its own file, so deleting that file removes the edge.
func shapesRepo(t *testing.T) *config.Repo {
	t.Helper()
	dir := testutil.SourceRepo(t, "shapes", map[string]string{
		"go.mod":   "module example.com/shapes\n\ngo 1.22\n",
		"i.go":     "package shapes\n\n// I is implemented by S.\ntype I interface{ M() }\n\n// S is a struct.\ntype S struct{}\n",
		"m.go":     "package shapes\n\n// M makes S an I.\nfunc (S) M() {}\n",
		"use.go":   "package shapes\n\n// Use calls M.\nfunc Use(i I) { i.M() }\n",
		"other.go": "package shapes\n\n// Other is unrelated.\nfunc Other() {}\n",
	})
	return &config.Repo{Path: dir, Name: "shapes"}
}

// edgeDump lists every symbol edge of the database at path, sorted.
func edgeDump(t *testing.T, path string) []string {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	rows, err := db.Query(`SELECT a.stable_key, e.kind, coalesce(b.stable_key, ''), e.confidence
		FROM edge e JOIN symbol a ON a.id = e.src_symbol_id LEFT JOIN symbol b ON b.id = e.dst_symbol_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var a, k, b, c string
		if err := rows.Scan(&a, &k, &b, &c); err != nil {
			t.Fatal(err)
		}
		out = append(out, strings.Join([]string{a, k, b, c}, " "))
	}
	sort.Strings(out)
	return out
}

// indexClean indexes rep into a new database and returns its path.
func indexClean(t *testing.T, rep *config.Repo, opts index.Options) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "clean.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if _, err := index.Repo(s, rep, opts); err != nil {
		t.Fatal(err)
	}
	if err := index.ResolvePending(s, opts); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestInterruptedMultiBatchSyncRecoversImplements interrupts a sync
// after its first write batch, which deleted the file declaring the
// method that made S an I, then checks the next Ensure restores the
// exact edge set of a clean index.
func TestInterruptedMultiBatchSyncRecoversImplements(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ws.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	rep := shapesRepo(t)
	opts := index.Options{Workspace: "w", MaxFiles: 10, RepoOrder: []string{rep.Name}}
	if _, err := index.Repo(s, rep, opts); err != nil {
		t.Fatal(err)
	}
	if err := index.ResolvePending(s, opts); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(rep.Path, "m.go")); err != nil {
		t.Fatal(err)
	}
	crash := errors.New("crash")
	restore := index.SetAfterBatch(func() error { return crash })
	if _, err := index.Ensure(s, rep, opts); !errors.Is(err, crash) {
		t.Fatalf("Ensure: %v, want the crash", err)
	}
	restore()
	if _, err := index.Ensure(s, rep, opts); err != nil {
		t.Fatal(err)
	}
	got, want := edgeDump(t, path), edgeDump(t, indexClean(t, rep, opts))
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("edges after recovery:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	wantPending(t, s, false)
}

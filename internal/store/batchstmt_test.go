package store

import (
	"cmp"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
)

// TestDeleteFilesPreparesItsStatementsOncePerShape deletes 1,200 files
// in one batch and checks what the batch prepared: the symbol and file
// deletes of a full IN list and of the rest, four statements, where one
// pair per file made a deleted tree of 50,000 files take seconds (most
// of it compiling the cascades of each DELETE again).
func TestDeleteFilesPreparesItsStatementsOncePerShape(t *testing.T) {
	s, repoID := openWithRepo(t)
	err := s.Batch(func(b *Batch) error {
		ids := make([]int64, 2*maxParams+200)
		var err error
		for i := range ids {
			if ids[i], err = b.UpsertFile(File{RepoID: repoID, Path: fmt.Sprintf("f%04d.txt", i), Hash: "h"}); err != nil {
				return err
			}
		}
		if len(b.stmts) != 1 {
			t.Errorf("%d statements prepared for %d upserts, want 1", len(b.stmts), len(ids))
		}
		if _, err := b.DeleteFiles(ids); err != nil {
			return err
		}
		if len(b.stmts) != 5 {
			t.Errorf("%d statements prepared, want the upsert and 4 deletes", len(b.stmts))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files, err := s.FilesByRepo(repoID); err != nil || len(files) != 0 {
		t.Fatalf("%d files left: %v", len(files), err)
	}
}

// TestSymbolsByKeyBasesPreparesOneStatement asks for the symbols of
// fewer bases than one statement takes, then of more, with a key that
// two bases in different statements both match (F and F#abcd): every
// call returns each symbol once, ordered by id, through one prepared
// statement, where one statement per number of bases made a first index
// compile a statement per file.
func TestSymbolsByKeyBasesPreparesOneStatement(t *testing.T) {
	s, repoID := openWithRepo(t)
	fileID, err := s.UpsertFile(File{RepoID: repoID, Path: "a.go", Lang: "go", Hash: "h"})
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{"r:go:p:F", "r:go:p:F#abcd", "r:go:p:G"}
	syms := make([]Symbol, len(keys))
	for i, k := range keys {
		syms[i] = Symbol{Kind: "function", Name: k, Qualified: k, StableKey: k, StartLine: i + 1, EndLine: i + 1,
			SigStartLine: i + 1, SigEndLine: i + 1, Visibility: "public", BodyHash: "b"}
	}
	if _, err := s.ReplaceFileSymbols(fileID, 0, syms); err != nil {
		t.Fatal(err)
	}
	many := []string{"r:go:p:F"}
	for i := range 2 * keyBasesPer {
		many = append(many, fmt.Sprintf("r:go:p:absent%03d", i))
	}
	many = append(many, "r:go:p:F#abcd", "r:go:p:G")
	err = s.Batch(func(b *Batch) error {
		for _, bases := range [][]string{nil, keys[2:], many} {
			got, err := b.SymbolsByKeyBases(bases, 0)
			if err != nil {
				return err
			}
			if want := map[int]int{0: 0, 1: 1, len(many): 3}[len(bases)]; len(got) != want {
				t.Errorf("%d bases: %d symbols, want %d", len(bases), len(got), want)
			}
			if !slices.IsSortedFunc(got, func(x, y Symbol) int { return cmp.Compare(x.ID, y.ID) }) {
				t.Errorf("%d bases: ids out of order: %+v", len(bases), got)
			}
		}
		if len(b.stmts) != 1 {
			t.Errorf("%d statements prepared, want 1", len(b.stmts))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// openWithRepo opens a new database holding one repo, closed when the
// test ends, and returns it with the repo's id.
func openWithRepo(t *testing.T) (*Store, int64) {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "ws.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	repoID, err := s.UpsertRepo(Repo{Workspace: "w", Name: "a", Path: "/a"})
	if err != nil {
		t.Fatal(err)
	}
	return s, repoID
}

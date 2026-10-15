package store_test

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/muralx/codedoc/internal/store"
)

// symbolFixture stores one repo with one file and returns their ids.
func symbolFixture(t *testing.T, s *store.Store) (repoID, fileID int64) {
	t.Helper()
	repoID, err := s.UpsertRepo(store.Repo{Workspace: "w", Name: "r", Path: "/r"})
	if err != nil {
		t.Fatal(err)
	}
	fileID, err = s.UpsertFile(store.File{RepoID: repoID, Path: "p/a.go", Lang: "go", Hash: "h"})
	if err != nil {
		t.Fatal(err)
	}
	return repoID, fileID
}

func sym(kind, qualified, container string, start, end int) store.Symbol {
	name := qualified
	if container != "" {
		name = qualified[len(container)+1:]
	}
	return store.Symbol{Kind: kind, Name: name, Qualified: qualified, Container: container,
		StableKey: "r:go:p:" + qualified, StartLine: start, EndLine: end, SigStartLine: start, SigEndLine: start,
		Signature: "sig " + qualified, Visibility: "public", BodyHash: "b", DocState: "missing"}
}

func sampleSymbols() []store.Symbol {
	doc := sym("struct", "T", "", 3, 10)
	doc.DocText, doc.DocHash, doc.DocState, doc.DocStartLine, doc.DocEndLine = "T is a thing.", "dh", "ok", 2, 2
	return []store.Symbol{sym("method", "T.Get", "T", 12, 14), doc, sym("field", "T.x", "T", 4, 4)}
}

func TestReplaceFileSymbolsStoresAndResolvesContainers(t *testing.T) {
	s := openTemp(t)
	repoID, fileID := symbolFixture(t, s)
	ids, err := s.ReplaceFileSymbols(fileID, 0, sampleSymbols())
	if err != nil || len(ids) != 3 {
		t.Fatalf("%v %v", ids, err)
	}
	got, err := s.SymbolByKey("r:go:p:T.Get")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != ids["r:go:p:T.Get"] || got.ContainerID != ids["r:go:p:T"] || got.Path != "p/a.go" ||
		got.RepoID != repoID || got.Lang != "go" || got.ModuleID != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestReplaceFileSymbolsKeepsDocsAndLineOrder(t *testing.T) {
	s := openTemp(t)
	_, fileID := symbolFixture(t, s)
	if _, err := s.ReplaceFileSymbols(fileID, 0, sampleSymbols()); err != nil {
		t.Fatal(err)
	}
	doc, _ := s.SymbolByKey("r:go:p:T")
	if doc.DocText != "T is a thing." || doc.DocState != "ok" || doc.DocStartLine != 2 || doc.ContainerID != 0 {
		t.Fatalf("%+v", doc)
	}
	in, err := s.SymbolsInFile(fileID)
	if err != nil || len(in) != 3 || in[0].Qualified != "T" || in[1].Qualified != "T.x" || in[2].Qualified != "T.Get" {
		t.Fatalf("%+v %v", in, err)
	}
}

func TestReplaceFileSymbolsReplaces(t *testing.T) {
	s := openTemp(t)
	_, fileID := symbolFixture(t, s)
	if _, err := s.ReplaceFileSymbols(fileID, 0, sampleSymbols()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReplaceFileSymbols(fileID, 0, []store.Symbol{sym("function", "F", "", 1, 2)}); err != nil {
		t.Fatal(err)
	}
	in, _ := s.SymbolsInFile(fileID)
	if len(in) != 1 || in[0].Qualified != "F" {
		t.Fatalf("%+v", in)
	}
	if _, err := s.SymbolByKey("r:go:p:T"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestSymbolByID(t *testing.T) {
	s := openTemp(t)
	_, fileID := symbolFixture(t, s)
	ids, err := s.ReplaceFileSymbols(fileID, 0, sampleSymbols())
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.SymbolByID(ids["r:go:p:T"])
	if err != nil || got.Qualified != "T" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := s.SymbolByID(999); !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestSymbolsByNameFiltersKindAndRepo(t *testing.T) {
	s := openTemp(t)
	repoID, fileID := symbolFixture(t, s)
	if _, err := s.ReplaceFileSymbols(fileID, 0, sampleSymbols()); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, kind string
		repo       int64
		want       int
	}{{"Get", "", 0, 1}, {"Get", "method", repoID, 1}, {"Get", "function", 0, 0}, {"Get", "", repoID + 1, 0}, {"Nope", "", 0, 0}} {
		got, err := s.SymbolsByName(c.name, c.kind, c.repo)
		if err != nil || len(got) != c.want {
			t.Errorf("%+v: %d %v", c, len(got), err)
		}
	}
}

func TestSymbolsByGlob(t *testing.T) {
	s := openTemp(t)
	repoID, fileID := symbolFixture(t, s)
	if _, err := s.ReplaceFileSymbols(fileID, 0, sampleSymbols()); err != nil {
		t.Fatal(err)
	}
	got, err := s.SymbolsByGlob("*e*", "", 0)
	if err != nil || len(got) != 1 || got[0].Name != "Get" {
		t.Fatalf("%+v %v", got, err)
	}
	got, _ = s.SymbolsByGlob("T.*", "field", repoID)
	if len(got) != 1 || got[0].Qualified != "T.x" {
		t.Fatalf("%+v", got)
	}
}

func TestSymbolByLocationReturnsInnermost(t *testing.T) {
	s := openTemp(t)
	_, fileID := symbolFixture(t, s)
	if _, err := s.ReplaceFileSymbols(fileID, 0, sampleSymbols()); err != nil {
		t.Fatal(err)
	}
	for line, want := range map[int]string{4: "T.x", 5: "T", 13: "T.Get"} {
		got, err := s.SymbolByLocation(fileID, line)
		if err != nil || got.Qualified != want {
			t.Errorf("line %d: %+v %v", line, got.Qualified, err)
		}
	}
	if _, err := s.SymbolByLocation(fileID, 11); !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestSymbolsInModule(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "ws.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	repoID, fileID := symbolFixture(t, s)
	raw, err := sql.Open("sqlite3", filepath.Join(dir, "ws.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	var modID int64
	if err := raw.QueryRow(`INSERT INTO module(repo_id, path, lang, kind, identity) VALUES(?, 'p', 'go', 'package', 'p')
  RETURNING id`, repoID).Scan(&modID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReplaceFileSymbols(fileID, modID, sampleSymbols()); err != nil {
		t.Fatal(err)
	}
	got, err := s.SymbolsInModule(modID)
	if err != nil || len(got) != 3 || got[0].ModuleID != modID {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestBatchSymbolKeyCollisionHelpers(t *testing.T) {
	s := openTemp(t)
	repoID, fileID := symbolFixture(t, s)
	other, _ := s.UpsertFile(store.File{RepoID: repoID, Path: "p/b.go", Lang: "go", Hash: "h"})
	a := sym("function", "F", "", 1, 2)
	b := sym("function", "F", "", 1, 2)
	b.StableKey = "r:go:p:F#abcd"
	if _, err := s.ReplaceFileSymbols(fileID, 0, []store.Symbol{a}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReplaceFileSymbols(other, 0, []store.Symbol{b}); err != nil {
		t.Fatal(err)
	}
	err := s.Batch(func(bt *store.Batch) error {
		hits, err := bt.SymbolsByKeyBases([]string{"r:go:p:F"}, other)
		if err != nil || len(hits) != 1 || hits[0].FileID != fileID {
			t.Fatalf("%+v %v", hits, err)
		}
		return bt.SetStableKey(hits[0].ID, "r:go:p:F#0001")
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SymbolByKey("r:go:p:F#0001"); err != nil {
		t.Fatal(err)
	}
}

func TestFileAndRepoByID(t *testing.T) {
	s := openTemp(t)
	repoID, fileID := symbolFixture(t, s)
	f, err := s.FileByID(fileID)
	if err != nil || f.Path != "p/a.go" {
		t.Fatalf("%+v %v", f, err)
	}
	r, err := s.RepoByID(repoID)
	if err != nil || r.Name != "r" || r.Workspace != "w" {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := s.FileByID(999); !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := s.RepoByID(999); !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
}

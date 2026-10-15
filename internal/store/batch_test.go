package store_test

import (
	"fmt"
	"testing"

	"github.com/muralx/codedoc/internal/store"
)

func TestBatchDeleteFilesReturnsTheSymbolsByFile(t *testing.T) {
	s := openTemp(t)
	repoID, fileID := symbolFixture(t, s)
	if _, err := s.ReplaceFileSymbols(fileID, 0, sampleSymbols()); err != nil {
		t.Fatal(err)
	}
	empty, err := s.UpsertFile(store.File{RepoID: repoID, Path: "p/empty.go", Hash: "h"})
	if err != nil {
		t.Fatal(err)
	}
	var gone map[int64][]store.Cleared
	err = s.Batch(func(b *store.Batch) error {
		var err error
		gone, err = b.DeleteFiles([]int64{fileID, empty})
		return err
	})
	if err != nil || len(gone) != 1 || len(gone[fileID]) != 3 || gone[fileID][0].Name == "" {
		t.Fatalf("%v %v", gone, err)
	}
	if in, _ := s.SymbolsInFile(fileID); len(in) != 0 {
		t.Fatalf("%+v", in)
	}
	if files, _ := s.FilesByRepo(repoID); len(files) != 0 {
		t.Fatalf("%+v", files)
	}
}

func TestBatchDeleteFilesSpansStatements(t *testing.T) {
	s := openTemp(t)
	repoID, _ := symbolFixture(t, s)
	before, _ := s.FilesByRepo(repoID)
	var ids []int64
	err := s.Batch(func(b *store.Batch) error {
		for i := range 1200 {
			id, err := b.UpsertFile(store.File{RepoID: repoID, Path: fmt.Sprintf("big/f%04d.txt", i), Hash: "h"})
			if err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = s.Batch(func(b *store.Batch) error {
		_, err := b.DeleteFiles(ids)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if files, _ := s.FilesByRepo(repoID); len(files) != len(before) {
		t.Fatalf("%d files left, want %d", len(files), len(before))
	}
}

func TestBatchKeyExists(t *testing.T) {
	s := openTemp(t)
	_, fileID := symbolFixture(t, s)
	if _, err := s.ReplaceFileSymbols(fileID, 0, sampleSymbols()); err != nil {
		t.Fatal(err)
	}
	err := s.Batch(func(b *store.Batch) error {
		for key, want := range map[string]bool{"r:go:p:T": true, "r:go:p:U": false} {
			got, err := b.KeyExists(key)
			if err != nil || got != want {
				t.Errorf("%s: %v %v", key, got, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

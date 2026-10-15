package index_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/store"
)

// staleFiles counts the file rows of a repo not extracted with the
// current extractor version.
func staleFiles(t *testing.T, s *store.Store, root string) (stale, total int) {
	t.Helper()
	row, err := s.RepoByPath(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := s.FilesByRepo(row.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.ExtractedVersion != index.ExtractorVersion() {
			stale++
		}
	}
	return stale, len(files)
}

// TestChunkedCatchUpAfterVersionBumpKeepsProgress stops a chunked
// catch-up after an extractor version bump once its first round is
// written, then checks the files of that round are recorded as extracted
// with the new version and the next Ensure re-extracts only the rest.
func TestChunkedCatchUpAfterVersionBumpKeepsProgress(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	indexRepo(t, s, rep)
	index.SetStoredExtractorVersion(t, s, rep.Path, "0")
	_, total := staleFiles(t, s, rep.Path)
	defer index.SetChunkFiles(2)()
	ctx, cancel := context.WithCancel(context.Background())
	defer index.SetAfterBatch(func() error { cancel(); return nil })()
	opts := index.WithBulk(index.Options{Workspace: "fixture", MaxFiles: 1000}, 1, nil)
	if _, err := index.EnsureContext(ctx, s, rep, opts); !errors.Is(err, context.Canceled) {
		t.Fatalf("EnsureContext: %v, want canceled after the first round", err)
	}
	if stale, _ := staleFiles(t, s, rep.Path); stale != total-2 {
		t.Fatalf("%d of %d files stale after one round of 2", stale, total)
	}
	index.SetAfterBatch(nil)
	f, err := index.Ensure(s, rep, opts)
	if err != nil || f.Changed != total-2 {
		t.Fatalf("Ensure re-extracted %d files (%v), want the %d left", f.Changed, err, total-2)
	}
	if stale, _ := staleFiles(t, s, rep.Path); stale != 0 {
		t.Fatalf("%d files stale after the catch-up", stale)
	}
	if f = ensure(t, s, rep, 1000); f.Changed != 0 {
		t.Fatalf("caught-up index re-extracted %d files", f.Changed)
	}
}

// TestMostFilesPendingResolvesInFull rewrites most files of a repo, one
// deleted, with
// the full-resolution threshold lowered, then checks the edges match a
// clean index.
func TestMostFilesPendingResolvesInFull(t *testing.T) {
	defer index.SetFullMinFiles(1)()
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
	for _, f := range []string{"i.go", "use.go", "other.go"} {
		writeFile(t, rep.Path, f, []byte(readFile(t, rep.Path, f)+"\n// edited\n"))
	}
	if _, err := index.Ensure(s, rep, opts); err != nil {
		t.Fatal(err)
	}
	got, want := edgeDump(t, path), edgeDump(t, indexClean(t, rep, opts))
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("edges:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

package index_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/store"
)

// TestEnsureFinishesAResolutionLeftPending interrupts a freshness sync
// between writing the files and resolving them, then checks the next
// Ensure resolves what the first one left pending.
func TestEnsureFinishesAResolutionLeftPending(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	opts := index.Options{Workspace: "w", MaxFiles: 10, RepoOrder: []string{rep.Name}}
	if _, err := index.Repo(s, rep, opts); err != nil {
		t.Fatal(err)
	}
	if err := index.ResolvePending(s, opts); err != nil {
		t.Fatal(err)
	}
	wantPending(t, s, false)
	src := "package service\n\nfunc X() string { return fallback(\"k\") }\n"
	if err := os.WriteFile(filepath.Join(rep.Path, "service", "x.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	crash := errors.New("crash")
	index.SetAfterSync(func() error { return crash })
	if _, err := index.Ensure(s, rep, opts); !errors.Is(err, crash) {
		t.Fatal(err)
	}
	index.SetAfterSync(nil)
	x, err := s.SymbolByKey("go-lib:go:github.com/fixture/lib/service:X")
	if err != nil {
		t.Fatal(err)
	}
	if out, _ := s.EdgesFrom(x.ID, "calls"); len(out) != 0 {
		t.Fatalf("resolution ran despite the crash: %v", out)
	}
	wantPending(t, s, true)
	if _, err := index.Ensure(s, rep, opts); err != nil {
		t.Fatal(err)
	}
	out, err := s.EdgesFrom(x.ID, "calls")
	if err != nil || len(out) != 1 || out[0].Confidence != "local" {
		t.Fatalf("X -> fallback after recovery: %v %v", out, err)
	}
	wantPending(t, s, false)
}

func wantPending(t *testing.T, s *store.Store, want bool) {
	t.Helper()
	if _, pending, err := s.Meta("pending_resolution"); err != nil || pending != want {
		t.Fatalf("pending %v, want %v (%v)", pending, want, err)
	}
}

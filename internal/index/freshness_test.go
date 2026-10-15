package index_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/store"
)

func ensure(t *testing.T, s *store.Store, rep *config.Repo, maxFiles int) index.Freshness {
	t.Helper()
	f, err := index.Ensure(s, rep, index.Options{Workspace: "fixture", MaxFiles: maxFiles})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestEnsureReextractsChangedFiles(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	indexRepo(t, s, rep)
	if f := ensure(t, s, rep, 200); !f.Fresh || f.Changed != 0 || f.Branch != "main" || f.Dirty != 0 {
		t.Fatalf("%+v", f)
	}
	appendFile(t, rep.Path, "store/null.go", "\n// Fresh is new.\nfunc Fresh() {}\n")
	f := ensure(t, s, rep, 200)
	if !f.Fresh || f.Changed != 1 || f.Dirty != 1 {
		t.Fatalf("%+v", f)
	}
	if _, err := s.SymbolByKey("go-lib:go:github.com/fixture/lib/store:Fresh"); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureOverLimitLeavesIndexUntouched(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	indexRepo(t, s, rep)
	appendFile(t, rep.Path, "store/null.go", "\nfunc A() {}\n")
	appendFile(t, rep.Path, "store/store.go", "\nfunc B() {}\n")
	f := ensure(t, s, rep, 0)
	if f.Fresh || f.Changed != 2 || f.Dirty != 2 {
		t.Fatalf("%+v", f)
	}
	if _, err := s.SymbolByKey("go-lib:go:github.com/fixture/lib/store:A"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("re-extracted over the limit: %v", err)
	}
	if f := ensure(t, s, rep, 2); !f.Fresh {
		t.Fatalf("%+v", f)
	}
	if _, err := s.SymbolByKey("go-lib:go:github.com/fixture/lib/store:A"); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureFindsAddedAndDeletedFiles(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	indexRepo(t, s, rep)
	writeFile(t, rep.Path, "store/added.go", []byte("package store\n\nfunc Added() {}\n"))
	if err := os.Remove(filepath.Join(rep.Path, "store", "null.go")); err != nil {
		t.Fatal(err)
	}
	if f := ensure(t, s, rep, 200); !f.Fresh || f.Changed != 1 {
		t.Fatalf("%+v", f)
	}
	if _, err := s.SymbolByKey("go-lib:go:github.com/fixture/lib/store:Added"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.SymbolsByName("NullStore", "", 0); len(got) != 0 {
		t.Fatalf("deleted file's symbols remain: %+v", got)
	}
}

func TestEnsureRefreshesHeadAfterCommit(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	indexRepo(t, s, rep)
	appendFile(t, rep.Path, "store/null.go", "\nfunc C() {}\n")
	ensure(t, s, rep, 200)
	gitCommitAll(t, rep.Path, "add C")
	f := ensure(t, s, rep, 200)
	r, _ := s.RepoByPath(rep.Path)
	if !f.Fresh || f.Dirty != 0 || r.Commit != f.Commit {
		t.Fatalf("%+v %+v", f, r)
	}
	if row := fileRow(t, s, rep.Path, "store/null.go"); row.LastCommit != f.Commit {
		t.Fatalf("last commit not refreshed: %+v", row)
	}
}

func TestEnsureIndexesUnknownRepoWithinLimit(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	if f := ensure(t, s, rep, 1); f.Fresh {
		t.Fatalf("%+v", f)
	}
	if _, err := s.RepoByPath(rep.Path); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("over-limit check wrote the repo row")
	}
	if f := ensure(t, s, rep, 200); !f.Fresh {
		t.Fatalf("%+v", f)
	}
	if _, err := s.SymbolByKey("go-lib:go:github.com/fixture/lib/store:MemStore.Get"); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkEnsure(b *testing.B) {
	root := b.TempDir()
	for i := 0; i < 5000; i++ {
		p := filepath.Join(root, fmt.Sprintf("d%d", i%50), fmt.Sprintf("f%d.go", i))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(fmt.Sprintf("package d\n\nfunc F%d() {}\n", i)), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	s, err := store.Open(filepath.Join(b.TempDir(), "ws.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	rep := &config.Repo{Path: root, Name: "bench"}
	if _, err := index.Repo(s, rep, index.Options{}); err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		if f, err := index.Ensure(s, rep, index.Options{MaxFiles: 200}); err != nil || !f.Fresh {
			b.Fatalf("%+v %v", f, err)
		}
	}
}

func TestEnsureRehashesRacilyCleanFiles(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	p := filepath.Join(rep.Path, "store", "null.go")
	// A file written just before it is indexed, unlike the fixture's.
	if err := os.Chtimes(p, time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	indexRepo(t, s, rep)
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	// Same size, same mtime: only the racy-timestamp rule catches it.
	if err := os.WriteFile(p, []byte(strings.ReplaceAll(string(src), "NullStore", "VoidStore")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if f := ensure(t, s, rep, 200); !f.Fresh || f.Changed != 1 {
		t.Fatalf("%+v", f)
	}
	if _, err := s.SymbolByKey("go-lib:go:github.com/fixture/lib/store:VoidStore"); err != nil {
		t.Fatal(err)
	}
}

// Two handles on one database, as two CLI processes would have, refresh
// the same repo at once: the write lock makes them apply the change one
// after the other, so both succeed and the change lands once.
func TestConcurrentEnsureSerializesWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ws.db")
	handles := make([]*store.Store, 2)
	for i := range handles {
		s, err := store.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = s.Close() }()
		handles[i] = s
	}
	rep := repoFixture(t, "go-lib")
	indexRepo(t, handles[0], rep)
	for round := 0; round < 5; round++ {
		appendFile(t, rep.Path, "store/null.go", fmt.Sprintf("\nfunc Round%d() {}\n", round))
		// The second writer re-checks under the lock and finds nothing left.
		if applied := ensureConcurrently(t, handles, rep); applied != 1 {
			t.Fatalf("round %d: change applied %d times", round, applied)
		}
		if _, err := handles[1].SymbolByKey(fmt.Sprintf("go-lib:go:github.com/fixture/lib/store:Round%d", round)); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
	}
}

// ensureConcurrently runs Ensure through every handle at once and returns
// the total of changed files they report.
func ensureConcurrently(t *testing.T, handles []*store.Store, rep *config.Repo) int {
	t.Helper()
	type result struct {
		f   index.Freshness
		err error
	}
	results := make(chan result, len(handles))
	for _, s := range handles {
		go func() {
			f, err := index.Ensure(s, rep, index.Options{Workspace: "fixture", MaxFiles: 200})
			results <- result{f, err}
		}()
	}
	applied := 0
	for range handles {
		r := <-results
		if r.err != nil {
			t.Fatal(r.err)
		}
		applied += r.f.Changed
	}
	return applied
}

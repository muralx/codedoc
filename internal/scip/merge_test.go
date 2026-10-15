package scip

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/store"
	"github.com/muralx/codedoc/internal/testutil"
)

const (
	keyLookup   = "go-lib:go:github.com/fixture/lib/service:Service.Lookup"
	keyTest     = "go-lib:go:github.com/fixture/lib/store:TestMemStorePutGet"
	keyStoreGet = "go-lib:go:github.com/fixture/lib/store:Store.Get"
	keyMemGet   = "go-lib:go:github.com/fixture/lib/store:MemStore.Get"
	keyStore    = "go-lib:go:github.com/fixture/lib/store:Store"
	keyMem      = "go-lib:go:github.com/fixture/lib/store:MemStore"
	keyNull     = "go-lib:go:github.com/fixture/lib/store:NullStore"
)

// libFixture is an indexed copy of the go-lib fixture.
type libFixture struct {
	s    *store.Store
	cfg  *config.Repo
	opts index.Options
}

func newLibFixture(t *testing.T) *libFixture {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "ws.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	f := &libFixture{s: s, cfg: &config.Repo{Path: testutil.CopyFixture(t, "go-lib"), Name: "go-lib"},
		opts: index.Options{Workspace: "fx", MaxFiles: 100, RepoOrder: []string{"go-lib"}}}
	if _, err := index.Repo(s, f.cfg, f.opts); err != nil {
		t.Fatal(err)
	}
	if err := index.ResolvePending(s, f.opts); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *libFixture) repo(t *testing.T) store.Repo {
	t.Helper()
	r, err := f.s.RepoByPath(f.cfg.Path)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// merge merges ix (the committed fixture index when nil) at the
// fixture's first commit.
func (f *libFixture) merge(t *testing.T, ix *Index) Stats {
	t.Helper()
	if ix == nil {
		ix = fixtureIndex(t)
	}
	r := f.repo(t)
	st, err := Merge(f.s, r, ix, gitHead(t, f.cfg.Path, "HEAD"))
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func (f *libFixture) ensure(t *testing.T) {
	t.Helper()
	if _, err := index.Ensure(f.s, f.cfg, f.opts); err != nil {
		t.Fatal(err)
	}
}

// edge returns the confidence of the edge of a kind from src to dst,
// counting ambiguous edges that list dst as a candidate, and how many
// such edges there are.
func (f *libFixture) edge(t *testing.T, src, dst, kind string) (string, int) {
	t.Helper()
	s, d := f.sym(t, src), f.sym(t, dst)
	es, err := f.s.EdgesFrom(s.ID, kind)
	if err != nil {
		t.Fatal(err)
	}
	conf, n := "", 0
	for _, e := range es {
		match := e.Dst == d.ID
		for _, c := range e.Candidates {
			match = match || c == d.ID
		}
		if match {
			conf, n = e.Confidence, n+1
		}
	}
	return conf, n
}

func (f *libFixture) sym(t *testing.T, key string) store.Symbol {
	t.Helper()
	sy, err := f.s.SymbolByKey(key)
	if err != nil {
		t.Fatalf("%s: %v", key, err)
	}
	return sy
}

func (f *libFixture) write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.cfg.Path, path), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *libFixture) appendTo(t *testing.T, path, text string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.cfg.Path, path))
	if err != nil {
		t.Fatal(err)
	}
	f.write(t, path, string(b)+text)
}

func fixtureIndex(t *testing.T) *Index {
	t.Helper()
	ix, err := ParseFile(fixtureIdx)
	if err != nil {
		t.Fatal(err)
	}
	return ix
}

func gitHead(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"rev-parse"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(out[:len(out)-1])
}

func gitCommitAll(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "-c", "user.email=t@x", "-c", "user.name=t", "commit", "-qam", "edit")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

func wantEdge(t *testing.T, f *libFixture, src, dst, kind, conf string) {
	t.Helper()
	if got, n := f.edge(t, src, dst, kind); got != conf || n != 1 {
		t.Fatalf("%s -%s-> %s: %q x%d, want %q x1", src, kind, dst, got, n, conf)
	}
}

func TestMergeFixtureMakesEdgesPrecise(t *testing.T) {
	f := newLibFixture(t)
	wantEdge(t, f, keyTest, keyMemGet, "calls", "local")
	wantEdge(t, f, keyLookup, keyStoreGet, "calls", "import")
	wantEdge(t, f, keyMem, keyStore, "implements", "local")
	st := f.merge(t, nil)
	if st.Files != 5 || st.Skipped != 0 || st.Mapped == 0 || st.Precise == 0 || st.Added == 0 || st.Implements != 2 {
		t.Fatalf("stats: %+v", st)
	}
	wantEdge(t, f, keyTest, keyMemGet, "calls", "precise")
	wantEdge(t, f, keyLookup, keyStoreGet, "calls", "precise")
	wantEdge(t, f, keyMem, keyStore, "implements", "precise")
	wantEdge(t, f, keyNull, keyStore, "implements", "precise")
	if r := f.repo(t); r.SCIPCommit != gitHead(t, f.cfg.Path, "HEAD") {
		t.Fatalf("scip_commit %q", r.SCIPCommit)
	}
	// A second merge replaces what the first stored.
	again := f.merge(t, nil)
	if again.Precise != st.Precise || again.Added != 0 || again.Mapped != st.Mapped || again.Dropped != 0 {
		t.Fatalf("second merge: %+v after %+v", again, st)
	}
	wantEdge(t, f, keyNull, keyStore, "implements", "precise")
}

func TestMergeSkipsFilesChangedSinceTheScipCommit(t *testing.T) {
	f := newLibFixture(t)
	commit := gitHead(t, f.cfg.Path, "HEAD")
	f.appendTo(t, "service/service.go", "\n// changed after the SCIP run\n")
	gitCommitAll(t, f.cfg.Path)
	f.ensure(t)
	st, err := Merge(f.s, f.repo(t), fixtureIndex(t), commit)
	if err != nil {
		t.Fatal(err)
	}
	if st.Skipped != 1 || st.Files != 4 {
		t.Fatalf("stats: %+v", st)
	}
	wantEdge(t, f, keyLookup, keyStoreGet, "calls", "import")
	wantEdge(t, f, keyTest, keyMemGet, "calls", "precise")
}

func TestMergeLeavesExternalSymbolsToHeuristicEdges(t *testing.T) {
	f := newLibFixture(t)
	ix := fixtureIndex(t)
	for i, o := range ix.Occurrences {
		if o.Path == "service/service.go" && o.Symbol == storeGet {
			ix.Occurrences[i].Symbol = "scip-go gomod example.com/ext v1.0.0 `example.com/ext`/Store#Get."
		}
	}
	f.merge(t, ix)
	wantEdge(t, f, keyLookup, keyStoreGet, "calls", "import")
	wantEdge(t, f, keyTest, keyMemGet, "calls", "precise")
}

func TestPreciseEdgesSurviveHeuristicResolution(t *testing.T) {
	f := newLibFixture(t)
	f.merge(t, nil)
	// A new symbol named Get re-resolves every reference named Get.
	f.write(t, "service/extra.go", "package service\n\n// Get is another Get.\nfunc Get() string { return \"\" }\n")
	f.ensure(t)
	wantEdge(t, f, keyLookup, keyStoreGet, "calls", "precise")
	wantEdge(t, f, keyTest, keyMemGet, "calls", "precise")
	// A full resolution keeps them too, without duplicating implements.
	if err := index.ResolvePending(f.s, f.opts); err != nil {
		t.Fatal(err)
	}
	wantEdge(t, f, keyLookup, keyStoreGet, "calls", "precise")
	wantEdge(t, f, keyMem, keyStore, "implements", "precise")
	// An edited file falls back to heuristic edges; the others keep theirs.
	f.appendTo(t, "service/service.go", "\n// edited\n")
	f.ensure(t)
	wantEdge(t, f, keyLookup, keyStoreGet, "calls", "import")
	wantEdge(t, f, keyTest, keyMemGet, "calls", "precise")
	wantEdge(t, f, keyNull, keyStore, "implements", "precise")
	// Implements edges of a type whose file changed are detected again.
	f.appendTo(t, "store/null.go", "\n// edited\n")
	f.ensure(t)
	wantEdge(t, f, keyNull, keyStore, "implements", "local")
	wantEdge(t, f, keyTest, keyMemGet, "calls", "precise")
}

func TestMergeWithoutCommitTrustsEveryFileStillPresent(t *testing.T) {
	f := newLibFixture(t)
	if err := os.Remove(filepath.Join(f.cfg.Path, "store", "null.go")); err != nil {
		t.Fatal(err)
	}
	st, err := Merge(f.s, f.repo(t), fixtureIndex(t), "")
	if err != nil {
		t.Fatal(err)
	}
	if st.Files != 4 || st.Skipped != 0 || st.Implements != 1 {
		t.Fatalf("stats: %+v", st)
	}
	wantEdge(t, f, keyLookup, keyStoreGet, "calls", "precise")
	if r := f.repo(t); r.SCIPCommit != "" {
		t.Fatalf("scip_commit %q", r.SCIPCommit)
	}
}

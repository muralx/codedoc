package scip

import (
	"context"
	"path/filepath"
	"slices"
	"sort"
	"testing"

	pb "github.com/scip-code/scip/bindings/go/scip"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/store"
	"github.com/muralx/codedoc/internal/testutil"
)

const libService = "scip-go gomod github.com/fixture/lib . `github.com/fixture/lib/service`/"

// appIndex is a go-app index: main and run defined, run calling
// Service.Lookup and main calling service.New of go-lib.
func appIndex() *pb.Index {
	const app = "scip-go gomod github.com/fixture/app . `github.com/fixture/app`/"
	def, ref := int32(pb.SymbolRole_Definition), int32(0)
	occ := func(line, col, end int32, sym string, role int32) *pb.Occurrence {
		return &pb.Occurrence{TypedRange: pb.NewRangeUnchecked([]int32{line - 1, col, end}).AsTypedRange(), Symbol: sym,
			SymbolRoles: role}
	}
	return &pb.Index{Documents: []*pb.Document{{RelativePath: "main.go", Occurrences: []*pb.Occurrence{
		occ(11, 5, 9, app+"main().", def),
		occ(12, 17, 20, libService+"New().", ref),
		occ(16, 5, 8, app+"run().", def),
		occ(16, 59, 65, libService+"Service#Lookup().", ref),
	}}}}
}

// preciseEdges indexes go-lib and go-app into a fresh database, runs
// SCIP over the repos in the order named and returns the precise edges,
// as source and target stable keys.
func preciseEdges(t *testing.T, stub string, names ...string) []string {
	t.Helper()
	t.Setenv("CODEDOC_SCIP_GO", stub)
	s, err := store.Open(filepath.Join(t.TempDir(), "ws.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	opts := index.Options{Workspace: "fx", RepoOrder: names}
	var rows []store.Repo
	for _, n := range names {
		cfg := &config.Repo{Path: testutil.CopyFixture(t, n), Name: n}
		if _, err := index.Repo(s, cfg, opts); err != nil {
			t.Fatal(err)
		}
		row, err := s.RepoByPath(cfg.Path)
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	if err := index.ResolvePending(s, opts); err != nil {
		t.Fatal(err)
	}
	if _, err := (Runner{}).RunRepos(context.Background(), s, rows); err != nil {
		t.Fatal(err)
	}
	return precise(t, s, rows)
}

// precise lists the precise edges leaving the symbols of rows.
func precise(t *testing.T, s *store.Store, rows []store.Repo) []string {
	t.Helper()
	var out []string
	for _, row := range rows {
		files, err := s.FilesByRepo(row.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			syms, err := s.SymbolsInFile(f.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, sy := range syms {
				out = append(out, preciseFrom(t, s, sy)...)
			}
		}
	}
	sort.Strings(out)
	return out
}

func preciseFrom(t *testing.T, s *store.Store, sy store.Symbol) []string {
	t.Helper()
	var out []string
	for _, kind := range []string{"calls", "references", "implements"} {
		es, err := s.EdgesFrom(sy.ID, kind)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range es {
			if e.Confidence == store.ConfidencePrecise {
				dst, err := s.SymbolByID(e.Dst)
				if err != nil {
					t.Fatal(err)
				}
				out = append(out, sy.StableKey+" -"+kind+"-> "+dst.StableKey)
			}
		}
	}
	return out
}

func TestRunReposIsOrderIndependent(t *testing.T) {
	lib, err := filepath.Abs(fixtureIdx)
	if err != nil {
		t.Fatal(err)
	}
	app := writeIndex(t, appIndex())
	indexer := stub(t, `if [ -f main.go ]; then cp '`+app+`' "$2"; else cp '`+lib+`' "$2"; fi`)
	appFirst := preciseEdges(t, indexer, "go-app", "go-lib")
	libFirst := preciseEdges(t, indexer, "go-lib", "go-app")
	if !slices.Equal(appFirst, libFirst) {
		t.Fatalf("go-app first:\n%v\ngo-lib first:\n%v", appFirst, libFirst)
	}
	want := "go-app:go:github.com/fixture/app:run -calls-> go-lib:go:github.com/fixture/lib/service:Service.Lookup"
	if !slices.Contains(appFirst, want) {
		t.Fatalf("no %s in %v", want, appFirst)
	}
}

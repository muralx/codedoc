package scip

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	pb "github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"
)

const (
	libStore   = "scip-go gomod github.com/fixture/lib . `github.com/fixture/lib/store`/"
	memGet     = libStore + "MemStore#Get()."
	storeGet   = libStore + "Store#Get."
	fixtureIdx = "testdata/go-lib.scip"
)

func findOcc(ix *Index, path string, line int, sym string) (Occurrence, bool) {
	for _, o := range ix.Occurrences {
		if o.Path == path && o.Line == line && o.Symbol == sym {
			return o, true
		}
	}
	return Occurrence{}, false
}

func TestParseFileFixtureDefinitionsAndRefs(t *testing.T) {
	ix, err := ParseFile(fixtureIdx)
	if err != nil {
		t.Fatal(err)
	}
	def, _ := findOcc(ix, "store/store.go", 26, memGet)
	wantDef := Occurrence{Path: "store/store.go", Line: 26, Col: 19, EndLine: 26, EndCol: 22, Symbol: memGet, Definition: true}
	if def != wantDef {
		t.Fatalf("MemStore.Get definition: %+v", def)
	}
	ref, ok := findOcc(ix, "service/service.go", 18, storeGet)
	if !ok || !ref.Ref || ref.Definition {
		t.Fatalf("Store.Get reference: %+v found=%v", ref, ok)
	}
	call, ok := findOcc(ix, "store/store_test.go", 10, memGet)
	if !ok || !call.Ref {
		t.Fatalf("MemStore.Get reference from test: %+v found=%v", call, ok)
	}
}

func TestParseFileFixtureSymbolsAndScope(t *testing.T) {
	ix, err := ParseFile(fixtureIdx)
	if err != nil {
		t.Fatal(err)
	}
	info := ix.Symbols[memGet]
	if info.DisplayName != "Get" || !slices.Contains(info.Implements, storeGet) {
		t.Fatalf("MemStore.Get info: %+v", info)
	}
	if !slices.Contains(ix.Symbols[libStore+"MemStore#"].Implements, libStore+"Store#") {
		t.Fatalf("MemStore implements Store: %+v", ix.Symbols[libStore+"MemStore#"])
	}
	for _, o := range ix.Occurrences {
		if !insideRoot(o.Path) || pb.IsLocalSymbol(o.Symbol) {
			t.Fatalf("occurrence outside root or local: %+v", o)
		}
	}
}

func writeIndex(t *testing.T, idx *pb.Index) string {
	t.Helper()
	b, err := proto.Marshal(idx)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "index.scip")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func handBuiltIndex() *pb.Index {
	const a, b, iface = "pkg a/A#", "pkg a/B().", "pkg a/I#"
	return &pb.Index{
		Metadata: &pb.Metadata{ProjectRoot: "file:///repo"},
		Documents: []*pb.Document{{
			RelativePath: "a/a.go",
			Occurrences: []*pb.Occurrence{
				{Range: []int32{2, 5, 6}, Symbol: a, SymbolRoles: int32(pb.SymbolRole_Definition)},
				{Range: []int32{4, 1, 6, 2}, Symbol: b, SymbolRoles: int32(pb.SymbolRole_ReadAccess)},
				{TypedRange: pb.NewRangeUnchecked([]int32{8, 2, 4}).AsTypedRange(), Symbol: b},
				{Range: []int32{0, 7, 12}, Symbol: iface, SymbolRoles: int32(pb.SymbolRole_Import)},
				{Range: []int32{5, 0, 3}, Symbol: "local 4"},
				{Range: []int32{6, 0, 3}},
			},
			Symbols: []*pb.SymbolInformation{
				{Symbol: a, DisplayName: "A", Relationships: []*pb.Relationship{
					{Symbol: iface, IsImplementation: true},
					{Symbol: b, IsReference: true},
					{Symbol: "local 9", IsImplementation: true},
				}},
				{Symbol: "local 4", DisplayName: "x"},
			},
		}, {
			RelativePath: "../cache/x.go",
			Occurrences:  []*pb.Occurrence{{Range: []int32{0, 0, 1}, Symbol: a}},
		}},
		ExternalSymbols: []*pb.SymbolInformation{{Symbol: iface, DisplayName: "I"}},
	}
}

func TestParseFileHandBuiltIndex(t *testing.T) {
	ix, err := ParseFile(writeIndex(t, handBuiltIndex()))
	if err != nil {
		t.Fatal(err)
	}
	want := []Occurrence{
		{Path: "a/a.go", Line: 3, Col: 5, EndLine: 3, EndCol: 6, Symbol: "pkg a/A#", Definition: true},
		{Path: "a/a.go", Line: 5, Col: 1, EndLine: 7, EndCol: 2, Symbol: "pkg a/B().", Ref: true},
		{Path: "a/a.go", Line: 9, Col: 2, EndLine: 9, EndCol: 4, Symbol: "pkg a/B().", Ref: true},
		{Path: "a/a.go", Line: 1, Col: 7, EndLine: 1, EndCol: 12, Symbol: "pkg a/I#"},
	}
	if !slices.Equal(ix.Occurrences, want) {
		t.Fatalf("occurrences:\n got %+v\nwant %+v", ix.Occurrences, want)
	}
	if got := ix.Symbols["pkg a/A#"]; got.DisplayName != "A" || !slices.Equal(got.Implements, []string{"pkg a/I#"}) {
		t.Fatalf("A info: %+v", got)
	}
	if got := ix.Symbols["pkg a/I#"]; got.DisplayName != "I" {
		t.Fatalf("external I info: %+v", got)
	}
	if _, ok := ix.Symbols["local 4"]; ok || len(ix.Symbols) != 2 {
		t.Fatalf("symbols: %+v", ix.Symbols)
	}
}

func TestParseFileErrors(t *testing.T) {
	if _, err := ParseFile(filepath.Join(t.TempDir(), "none.scip")); err == nil {
		t.Fatal("missing file: no error")
	}
	bad := &pb.Index{Documents: []*pb.Document{{
		RelativePath: "a.go",
		Occurrences:  []*pb.Occurrence{{Range: []int32{1, 2}, Symbol: "pkg a/A#"}},
	}}}
	if _, err := ParseFile(writeIndex(t, bad)); err == nil {
		t.Fatal("two-element range: no error")
	}
	junk := filepath.Join(t.TempDir(), "junk.scip")
	if err := os.WriteFile(junk, []byte{0x12, 0xff, 0xff, 0xff, 0xff, 0x0f, 1}, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseFile(junk); err == nil {
		t.Fatal("truncated index: no error")
	}
}

func TestInsideRoot(t *testing.T) {
	for p, want := range map[string]bool{
		"a/b.go": true, "./a.go": true, "../x.go": false, "..": false, ".": false, "/abs.go": false, "a/../../b": false,
	} {
		if got := insideRoot(p); got != want {
			t.Errorf("insideRoot(%q) = %v, want %v", p, got, want)
		}
	}
}

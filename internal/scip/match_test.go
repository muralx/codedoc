package scip

import (
	"slices"
	"testing"

	"github.com/muralx/codedoc/internal/store"
)

func TestFamilies(t *testing.T) {
	got := Families([]string{"tsx", "go", "yaml", "", "javascript", "python", "go", "java"})
	if !slices.Equal(got, []string{"go", "java", "py", "ts"}) {
		t.Fatalf("families: %v", got)
	}
}

func TestIdentifiersReadBytesRunesAndUTF16(t *testing.T) {
	lines := []string{"x := Get(k)", "s := \"é\"; Put(v)", "😀 Put"}
	cases := []struct {
		o    Occurrence
		want []string
	}{
		{Occurrence{Line: 1, Col: 5, EndLine: 1, EndCol: 8}, []string{"Get"}},
		{Occurrence{Line: 2, Col: 10, EndLine: 2, EndCol: 13}, []string{" Pu", "Put"}},
		{Occurrence{Line: 3, Col: 3, EndLine: 3, EndCol: 6}, []string{"Put", "\x80 P"}},
		{Occurrence{Line: 4, Col: 0, EndLine: 4, EndCol: 1}, nil},
		{Occurrence{Line: 1, Col: 5, EndLine: 2, EndCol: 1}, nil},
		{Occurrence{Line: 1, Col: 5, EndLine: 1, EndCol: 99}, nil},
	}
	for _, c := range cases {
		if got := sortedNames(identifiers(lines, c.o)); !slices.Equal(got, c.want) {
			t.Errorf("%+v: %q, want %q", c.o, got, c.want)
		}
	}
}

func TestSymbolKeyDropsTheVersion(t *testing.T) {
	a := symbolKey("scip-go gomod example.com/m v1.2.0 `example.com/m`/T#")
	b := symbolKey("scip-go gomod example.com/m . `example.com/m`/T#")
	if a != b || a != "scip-go gomod example.com/m `example.com/m`/T#" {
		t.Fatalf("keys: %q %q", a, b)
	}
	for _, s := range []string{"local 3", "a  b c d e"} {
		if symbolKey(s) != s {
			t.Errorf("%q changed to %q", s, symbolKey(s))
		}
	}
}

func TestRefKindAndAddedName(t *testing.T) {
	for sym, want := range map[string]string{"p/f().": "call", "p/T#": "type", "p/T#f.": "field"} {
		if got := refKind(sym, "struct"); got != want {
			t.Errorf("%s: %s, want %s", sym, got, want)
		}
	}
	if refKind("p/I#M.", "method") != "call" {
		t.Error("interface method reference is a call")
	}
	if addedName(map[string]bool{"Alias": true}, "Name") != "Alias" ||
		addedName(map[string]bool{"Name": true, "x": true}, "Name") != "Name" || addedName(nil, "Name") != "Name" {
		t.Error("added names")
	}
}

func TestInnermostPrefersTheSmallestLaterSpan(t *testing.T) {
	syms := []store.Symbol{{ID: 1, Name: "T", StartLine: 1, EndLine: 10}, {ID: 2, Name: "a", StartLine: 3, EndLine: 3},
		{ID: 3, Name: "b", StartLine: 3, EndLine: 3}, {ID: 4, Name: "c", StartLine: 2, EndLine: 3}}
	all := func(store.Symbol) bool { return true }
	if s, _ := innermost(syms, 3, all); s.ID != 2 {
		t.Fatalf("innermost: %+v", s)
	}
	if s, _ := innermost(syms, 5, all); s.ID != 1 {
		t.Fatalf("enclosing: %+v", s)
	}
	if _, ok := innermost(syms, 11, all); ok {
		t.Fatal("outside every symbol")
	}
}

func TestChangedFilesWithoutCommitIsEmpty(t *testing.T) {
	got, err := changedFiles(t.TempDir(), "")
	if err != nil || len(got) != 0 {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := changedFiles(t.TempDir(), "HEAD"); err == nil {
		t.Fatal("not a repository")
	}
}

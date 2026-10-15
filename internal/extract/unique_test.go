package extract_test

import (
	"reflect"
	"testing"

	"github.com/muralx/codedoc/internal/extract"
)

func TestUniqueQualified(t *testing.T) {
	syms := []extract.Symbol{{Qualified: "a"}, {Qualified: "b"}, {Qualified: "a"}, {Qualified: "a"}, {Qualified: "a~2"}}
	extract.UniqueQualified(syms)
	var got []string
	for _, s := range syms {
		got = append(got, s.Qualified)
	}
	want := []string{"a", "b", "a~2", "a~3", "a~2~2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestUniqueQualifiedKeepsOverloads(t *testing.T) {
	syms := []extract.Symbol{
		{Qualified: "C.put", SigHash: "1a2b"}, {Qualified: "C.put", SigHash: "3c4d"},
		{Qualified: "C.put", SigHash: "1a2b"}, {Qualified: "C.put"},
	}
	extract.UniqueQualified(syms)
	var got []string
	for _, s := range syms {
		got = append(got, s.Qualified)
	}
	want := []string{"C.put", "C.put", "C.put~2", "C.put"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

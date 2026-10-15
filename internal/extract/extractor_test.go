package extract_test

import (
	"testing"

	"github.com/muralx/codedoc/internal/extract"
)

type fake struct{}

func (fake) Extract(string, []byte) (*extract.FileResult, error) { return &extract.FileResult{}, nil }

func TestRegistry(t *testing.T) {
	if e, ok := extract.For("none"); ok || e != nil {
		t.Fatal("unknown language must not resolve")
	}
	extract.Register("fake-lang", fake{})
	if e, ok := extract.For("fake-lang"); !ok || e == nil {
		t.Fatal("registered extractor not found")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate Register must panic")
		}
	}()
	extract.Register("fake-lang", fake{})
}

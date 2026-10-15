package testutil_test

import (
	"maps"
	"slices"
	"testing"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/testutil"
)

func TestConstConfigRefsAndSymbolValues(t *testing.T) {
	refs := []extract.Reference{
		extract.ConfigConstRef("KEY", "f", 3, 7),
		extract.ConfigRef("LIT", true, "f", 4, 1),
		{Kind: extract.RefField, Name: "KEY", Qualifier: extract.ConfigAccessorConst},
	}
	if got := testutil.ConstConfigRefs(refs); !slices.Equal(got, []string{"KEY@3:7"}) {
		t.Errorf("const config refs %v", got)
	}
	syms := []extract.Symbol{{Qualified: "a.KEY", Value: "X"}, {Qualified: "a.f"}}
	if got := testutil.SymbolValues(syms); !maps.Equal(got, map[string]string{"a.KEY": "X"}) {
		t.Errorf("values %v", got)
	}
}

package query_test

import (
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/query"
)

func TestGapsDropWhatTheWriterRefuses(t *testing.T) {
	c := tempWorkspace(t, map[string]string{"m.py": "LIMIT = 1\n\n\ndef compute(x):\n    y = x + 1\n    return y\n"})
	for _, limit := range []int{0, 2} {
		r, err := query.Gaps(c, "", query.GapsOpts{Kind: "missing", Limit: limit})
		text := run(t, r, err)
		if strings.Contains(text, "LIMIT") || !strings.Contains(text, "compute") {
			t.Fatalf("limit %d: a Python constant cannot carry a docstring:\n%s", limit, text)
		}
	}
}

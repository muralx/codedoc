package index_test

import (
	"testing"

	"github.com/muralx/codedoc/internal/index"
)

func TestOversizedIsAboveTheSizeLimit(t *testing.T) {
	limit := index.SizeLimit()
	if limit != 2<<20 || index.Oversized(limit) || !index.Oversized(limit+1) {
		t.Fatalf("limit %d: at it %v, above it %v", limit, index.Oversized(limit), index.Oversized(limit+1))
	}
}

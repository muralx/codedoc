package index_test

import (
	"testing"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/store"
)

// The store selects accessor reads by the qualifier the extractors write.
func TestConfigAccessorQualifierAgrees(t *testing.T) {
	if extract.ConfigAccessor != store.ConfigAccessor {
		t.Fatalf("extract %q, store %q", extract.ConfigAccessor, store.ConfigAccessor)
	}
}

package kit

import "testing"

func TestNew(t *testing.T) { _ = New("x") }

// ExportedFromTest must never be indexed.
func ExportedFromTest() {}

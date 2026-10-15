package output_test

import (
	"strings"
	"testing"
)

// TestBareResponseHasNoHeader checks that an answer describing no index
// state starts at its first section in both formats, and is cut by the
// budget like any other.
func TestBareResponseHasNoHeader(t *testing.T) {
	r := tenEntries()
	r.Bare = true
	text, omitted := render(t, r, "text", 0)
	if !strings.HasPrefix(text, "== callers (30)\na.go\n  auth:go:pkg:F0  :10  ") || omitted != 0 {
		t.Fatalf("text:\n%s", text)
	}
	text, omitted = render(t, r, "text", 200)
	if len(text) > 200 || omitted == 0 || !strings.HasPrefix(text, "== callers (30)\n") ||
		!strings.Contains(text, " more: rerun with --offset 2") {
		t.Fatalf("budgeted text (%d omitted):\n%s", omitted, text)
	}
	js, _ := render(t, r, "json", 0)
	if !strings.HasPrefix(js, `{"sections":[{"title":"callers","entries":[{"key":"auth:go:pkg:F0"`) {
		t.Fatalf("json: %s", js)
	}
	js, omitted = render(t, r, "json", 300)
	if len(js) > 300 || omitted == 0 || strings.Contains(js, `"header"`) || !strings.Contains(js, `"next_offset":2`) {
		t.Fatalf("budgeted json (%d omitted): %s", omitted, js)
	}
}

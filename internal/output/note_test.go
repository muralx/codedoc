package output_test

import (
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/output"
)

// A note that does not fit is never cut inside a word or a command: its
// sentences are kept whole while they fit, the candidates of an also
// line are dropped one by one and counted, and what is left out is left
// out whole.
func TestNoteIsNeverCutInsideAnItem(t *testing.T) {
	const also = "also: Store (java-lib:src/main/java/com/fixture/store/Store.java:8), Store (py-lib:src/pylib/store.py:13), " +
		"Store (ts-lib:src/store.ts:4), +2 more"
	const outline = "Store is 7 lines, over the budget: its members are listed; codedoc symbol Store.<member> prints one"
	r := &output.Response{Bare: true, Note: "answered for Store. " + also + ". " + outline}
	for budget, want := range map[int]string{
		0:   r.Note,
		400: r.Note,
		// The last sentence does not fit: it is left out whole.
		230: "answered for Store. " + also,
		// The also line loses candidates from its end and counts them.
		150: "answered for Store. also: Store (java-lib:src/main/java/com/fixture/store/Store.java:8), " +
			"Store (py-lib:src/pylib/store.py:13), +3 more",
		110: "answered for Store. also: Store (java-lib:src/main/java/com/fixture/store/Store.java:8), +4 more",
		// Not one candidate fits: the line is left out.
		60: "answered for Store",
		10: "",
	} {
		got, _ := render(t, r, "text", budget)
		if got = strings.TrimSuffix(got, "\n"); got != want || (budget > 0 && len(got) >= budget) {
			t.Errorf("budget %d: %q", budget, got)
		}
	}
	// A note of one long sentence is printed whole or not at all.
	r.Note = strings.Repeat("stale ", 100)
	if got, _ := render(t, r, "text", 100); got != "" {
		t.Errorf("%q", got)
	}
}

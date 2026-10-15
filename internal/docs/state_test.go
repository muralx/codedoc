package docs_test

import (
	"math"
	"testing"

	"github.com/muralx/codedoc/internal/docs"
)

func TestNext(t *testing.T) {
	never := func() docs.Seed { t.Fatal("seed consulted"); return 0 }
	seed := func(s docs.Seed) func() docs.Seed { return func() docs.Seed { return s } }
	for _, tc := range []struct {
		name      string
		prior     *docs.Prior
		doc, body string
		seed      func() docs.Seed
		wantHash  string
		wantState docs.State
	}{
		{"no doc", nil, "", "b1", never, "", docs.Missing},
		{"no doc with prior", &docs.Prior{DocHash: "d", BodyHash: "b0", DocBodyHash: "b0"}, "", "b1", never, "", docs.Missing},
		{"new, untracked", nil, "d", "b1", seed(docs.SeedUntracked), "b1", docs.OK},
		{"new, doc current", nil, "d", "b1", seed(docs.SeedDocCurrent), "b1", docs.OK},
		{"new, body newer", nil, "d", "b1", seed(docs.SeedBodyNewer), "", docs.Stale},
		{"body changed", &docs.Prior{DocHash: "d", BodyHash: "b0", DocBodyHash: "b0"}, "d", "b1", never, "b0", docs.Stale},
		{"doc edited", &docs.Prior{DocHash: "d0", BodyHash: "b0", DocBodyHash: "b0"}, "d", "b1", seed(docs.SeedDocCurrent), "b1", docs.OK},
		{"doc checked out older than body", &docs.Prior{DocHash: "d0", BodyHash: "b1", DocBodyHash: "b1"}, "d", "b1",
			seed(docs.SeedBodyNewer), "", docs.Stale},
		{"doc added", &docs.Prior{BodyHash: "b0"}, "d", "b1", seed(docs.SeedUntracked), "b1", docs.OK},
		{"unchanged", &docs.Prior{DocHash: "d", BodyHash: "b1", DocBodyHash: "b1"}, "d", "b1", never, "b1", docs.OK},
		{"seeded stale stays stale", &docs.Prior{DocHash: "d", BodyHash: "b1"}, "d", "b1", never, "", docs.Stale},
		{"body changed back", &docs.Prior{DocHash: "d", BodyHash: "b1", DocBodyHash: "b0"}, "d", "b0", never, "b0", docs.OK},
	} {
		h, st := docs.Next(tc.prior, tc.doc, tc.body, tc.seed)
		if h != tc.wantHash || st != tc.wantState {
			t.Errorf("%s: got (%q, %s), want (%q, %s)", tc.name, h, st, tc.wantHash, tc.wantState)
		}
	}
}

func TestSeedFromBlame(t *testing.T) {
	blame := map[int]int64{1: 100, 2: 100, 3: 100, 4: 200, 5: 100, 6: math.MaxInt64}
	doc := docs.Lines{Start: 1, End: 2}
	for _, tc := range []struct {
		name string
		span docs.Lines
		doc  docs.Lines
		want docs.Seed
	}{
		{"body older or equal", docs.Lines{Start: 3, End: 3}, doc, docs.SeedDocCurrent},
		{"body newer", docs.Lines{Start: 3, End: 5}, doc, docs.SeedBodyNewer},
		{"uncommitted body", docs.Lines{Start: 5, End: 6}, doc, docs.SeedBodyNewer},
		{"uncommitted doc", docs.Lines{Start: 3, End: 5}, docs.Lines{Start: 6, End: 6}, docs.SeedDocCurrent},
		{"docstring inside the span is skipped", docs.Lines{Start: 1, End: 3}, docs.Lines{Start: 2, End: 2}, docs.SeedDocCurrent},
		{"no blame lines", docs.Lines{Start: 10, End: 12}, doc, docs.SeedDocCurrent},
	} {
		if got := docs.SeedFromBlame(blame, tc.span, tc.doc); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
	// A container skips its members' bodies and docs: only line 3 (its
	// header) and line 5 count against the doc.
	if got := docs.SeedFromBlame(blame, docs.Lines{Start: 3, End: 5}, doc, docs.Lines{Start: 4, End: 4}); got != docs.SeedDocCurrent {
		t.Errorf("skipped member body: got %d", got)
	}
}

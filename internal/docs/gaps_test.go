package docs_test

import (
	"math"
	"testing"
	"time"

	"github.com/muralx/codedoc/internal/docs"
)

var day0 = time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)

func TestScore(t *testing.T) {
	recent := day0.Add(-3 * 24 * time.Hour).Unix()
	old := day0.Add(-90 * 24 * time.Hour).Unix()
	for _, tc := range []struct {
		name string
		c    docs.Candidate
		want float64
	}{
		{"public function, no fan-in, old", docs.Candidate{Kind: "function", Visibility: "public", ChangedAt: old}, 3},
		{"package method fan-in 1", docs.Candidate{Kind: "method", Visibility: "package", FanIn: 1}, 1.5 * (1 + math.Log(2))},
		{"private struct, recent", docs.Candidate{Kind: "struct", Visibility: "private", ChangedAt: recent}, 2 * 1.5},
		{"interface uncommitted", docs.Candidate{Kind: "interface", Visibility: "public", ChangedAt: math.MaxInt64}, 3 * 3 * 1.5},
		{"field", docs.Candidate{Kind: "field", Visibility: "public"}, 1.5},
		{"module fan-in 3, 30 files", docs.Candidate{Kind: "module", FanIn: 3, Files: 30}, 3 * (1 + math.Log(4)) * 4},
		{"module of one file", docs.Candidate{Kind: "module", Files: 1}, 3 * (1 + math.Log(2))},
		{"trait", docs.Candidate{Kind: "trait", Visibility: "private"}, 3},
		{"class", docs.Candidate{Kind: "class", Visibility: "private"}, 2},
		{"const", docs.Candidate{Kind: "const", Visibility: "private"}, 1},
	} {
		if got := docs.Score(tc.c, day0); math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestRankOrderExclusionsAndReasons(t *testing.T) {
	long := 10
	cands := []docs.Candidate{
		{Key: "missing-high", Kind: "interface", Visibility: "public", State: docs.Missing, Lines: long, FanIn: 9},
		{Key: "stale-low", Kind: "field", Visibility: "private", State: docs.Stale, Lines: long},
		{Key: "b-tie", Kind: "function", Visibility: "public", State: docs.Missing, Lines: long},
		{Key: "a-tie", Kind: "function", Visibility: "public", State: docs.Missing, Lines: long,
			ChangedAt: day0.Add(-50 * 24 * time.Hour).Unix()},
		{Key: "ok", Kind: "function", Visibility: "public", State: docs.OK, Lines: long},
		{Key: "test", Kind: "function", Visibility: "public", State: docs.Missing, Lines: long, Test: true},
		{Key: "gen", Kind: "function", Visibility: "public", State: docs.Missing, Lines: long, Generated: true},
		{Key: "trivial", Kind: "method", Visibility: "public", State: docs.Missing, Lines: long, Trivial: true},
		{Key: "short-private", Kind: "function", Visibility: "private", State: docs.Missing, Lines: 2},
		{Key: "short-public", Kind: "function", Visibility: "public", State: docs.Missing, Lines: 1,
			ChangedAt: math.MaxInt64},
		{Key: "iface-method", Kind: "method", Visibility: "public", State: docs.Missing, Lines: long, DocumentedInterface: true},
		{Key: "mod", Kind: "module", State: docs.Missing, Files: 9, ChangedAt: day0.Add(-2 * time.Hour).Unix()},
	}
	got := docs.Rank(cands, day0)
	var keys []string
	for _, g := range got {
		keys = append(keys, g.Key)
	}
	want := []string{"stale-low", "missing-high", "mod", "short-public", "a-tie", "b-tie"}
	if len(keys) != len(want) {
		t.Fatalf("got %v, want %v", keys, want)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("got %v, want %v", keys, want)
		}
	}
	reasons := map[string]string{"missing-high": "public, fan-in 9", "mod": "module, fan-in 0, changed today",
		"short-public": "public, fan-in 0, uncommitted change", "a-tie": "public, fan-in 0, changed 50d ago"}
	for _, g := range got {
		if r, ok := reasons[g.Key]; ok && g.Reason != r {
			t.Errorf("%s: reason %q, want %q", g.Key, g.Reason, r)
		}
	}
}

func TestRankExcludesFixturesVendorAndSynthetic(t *testing.T) {
	cands := []docs.Candidate{
		{Key: "fixture", Kind: "function", Visibility: "public", State: docs.Missing, Lines: 10, Path: "testdata/ws/a.go"},
		{Key: "vendored", Kind: "function", Visibility: "public", State: docs.Missing, Lines: 10, Path: "vendor/x/a.go"},
		{Key: "example", Kind: "module", State: docs.Missing, Path: "examples/basic"},
		{Key: "synthetic", Kind: "method", Visibility: "public", State: docs.Missing, Lines: 10, Synthetic: true},
		{Key: "kept", Kind: "function", Visibility: "public", State: docs.Missing, Lines: 10, Path: "lib/a.go"},
	}
	if got := docs.Rank(cands, day0); len(got) != 1 || got[0].Key != "kept" {
		t.Fatalf("got %+v", got)
	}
}

func TestTrivialBody(t *testing.T) {
	for _, tc := range []struct {
		lang, src string
		want      bool
	}{
		{"go", "func (m *M) Name() string { return m.name }", true},
		{"go", "func (m *M) Name() string {\n\treturn m.name\n}", true},
		{"java", "public void setName(String name) {\n  this.name = name;\n}", true},
		{"java", "public int size() { return items.size(); }", false},
		{"typescript", "get id() { return this._id; }", true},
		{"python", "def name(self):\n    return self._name", true},
		{"python", "def name(self): return self._name", true},
		{"python", "def name(self):\n    x = 1\n    return x", false},
		{"go", "func F()", false},
		{"python", "lambda", false},
	} {
		if got := docs.TrivialBody(tc.lang, tc.src); got != tc.want {
			t.Errorf("%s %q: %v", tc.lang, tc.src, got)
		}
	}
}

func TestIsTestPath(t *testing.T) {
	for p, want := range map[string]bool{
		"store/store_test.go": true, "tests/test_store.py": true, "pkg/test_x.py": true, "pkg/x_test.py": true,
		"src/records.test.ts": true, "src/a.spec.js": true, "src/__tests__/a.ts": true,
		"src/test/java/A.java": true, "lib/src/test/A.java": true, "tests": true,
		"store/store.go": false, "src/main/java/A.java": false, "pkg/contest.py": false, "testing/x.go": false,
	} {
		if got := docs.IsTestPath(p); got != want {
			t.Errorf("%s: %v", p, got)
		}
	}
}

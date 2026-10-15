package query

import (
	"fmt"
	"slices"
	"testing"

	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/store"
)

func TestSharesGrantSmallSizesWholeAndSplitTheRest(t *testing.T) {
	for _, c := range []struct {
		sizes []int
		room  int
		want  []int
	}{
		{[]int{100, 200, 300}, 1000, []int{100, 200, 300}},  // everything fits
		{[]int{100, 5000, 300}, 1000, []int{100, 600, 300}}, // one large section takes what the small ones leave
		{[]int{100, 5000, 5000}, 1000, []int{100, 450, 450}},
		{[]int{400, 400, 400}, 900, []int{300, 300, 300}}, // none fits: equal shares
		{[]int{50, 400, 300}, 600, []int{50, 275, 275}},
		{[]int{50, 400}, 0, []int{1, 1}}, // no room: a byte each, never the zero that means no limit
		{nil, 100, []int{}},
	} {
		if got := shares(c.sizes, c.room); !slices.Equal(got, c.want) {
			t.Errorf("shares(%v, %d) = %v, want %v", c.sizes, c.room, got, c.want)
		}
	}
}

// callers returns one caller entry per count: the entry of file f with
// that many call lines, starting at line 10.
func callerEntries(f string, counts ...int) ([]output.Entry, map[string]int) {
	sites := map[string]int{}
	var out []output.Entry
	for i, n := range counts {
		e := output.Entry{Key: fmt.Sprintf("%s#%d", f, i), Loc: f + ":10", Site: true}
		text := ""
		for l := range n {
			text += fmt.Sprintf("call%d()\n", l)
		}
		e.Fragments = []output.Fragment{{Start: 10, End: 9 + n, Text: text}}
		sites[e.Key] = n
		out = append(out, e)
	}
	return out, sites
}

// crowdedCallers returns the callers of three files: a.go with ten call
// sites, b.go with twelve in four callers and c.go with twelve in one;
// and the call sites of each caller.
func crowdedCallers() (quiet, busy, one []output.Entry, sites map[string]int) {
	quiet, s1 := callerEntries("a.go", 4, 6)
	busy, s2 := callerEntries("b.go", 1, 5, 2, 4)
	one, s3 := callerEntries("c.go", 12)
	sites = map[string]int{}
	for _, m := range []map[string]int{s1, s2, s3} {
		for k, v := range m {
			sites[k] = v
		}
	}
	return quiet, busy, one, sites
}

func TestCrowdKeepsThreeCallSitesOfACrowdedFile(t *testing.T) {
	quiet, busy, one, sites := crowdedCallers()
	entries := append(append(quiet, busy...), one...)
	got := crowd(entries, crowding{sites: sites, what: "call sites in this file", command: "codedoc callers X", offset: 5})
	// A file of ten call sites stays whole.
	if len(got) != 5 || got[0].Covers != 0 || got[1].More != "" {
		t.Fatalf("a.go: %+v", got)
	}
	// b.go: the first caller whole, two of the five lines of the second;
	// that line stands for itself and the two callers left out, and the
	// command starts again at the caller that was cut (rank 5+3).
	want := output.Entry{Covers: 3, More: "9 more call sites in this file: codedoc callers X --offset 8"}
	if e := got[3]; e.Covers != want.Covers || e.More != want.More || e.Fragments[0].Text != "call0()\ncall1()\n" {
		t.Fatalf("b.go: %+v", e)
	}
	// c.go: one caller with twelve calls is cut to three and covers only
	// itself.
	want = output.Entry{Covers: 1, More: "9 more call sites in this file: codedoc callers X --offset 11"}
	if e := got[4]; e.Covers != want.Covers || e.More != want.More || e.Fragments[0].End != 12 {
		t.Fatalf("c.go: %+v", e)
	}
}

func TestCrowdKeepsWholeEntriesAndGroupsByDirectory(t *testing.T) {
	_, busy, _, sites := crowdedCallers()
	// With wide fragments a caller is kept or left out as a whole.
	wide := crowd(busy, crowding{sites: sites, what: "call sites in this file", command: "cmd", wide: true})
	want := output.Entry{Covers: 3, More: "6 more call sites in this file: cmd --offset 2"}
	if len(wide) != 2 || wide[1].Fragments[0].End != 14 || wide[1].Covers != want.Covers || wide[1].More != want.More {
		t.Fatalf("wide: %+v", wide)
	}
	// Entries without a count are one each, grouped by what dir says.
	var mods []output.Entry
	for i := range 12 {
		mods = append(mods, output.Entry{Loc: fmt.Sprintf("pkg/locales/l%d.js", i)})
	}
	got := crowd(mods, crowding{what: "modules in this directory", command: "codedoc outline pkg", wide: true,
		dir: func(output.Entry) string { return "pkg/locales/" }})
	want = output.Entry{Covers: 10, More: "9 more modules in this directory: codedoc outline pkg --offset 3"}
	if len(got) != 3 || got[2].Covers != want.Covers || got[2].More != want.More {
		t.Fatalf("modules: %+v", got)
	}
}

func TestMentionCountsNamesAsTheCodeWritesThem(t *testing.T) {
	member := mention{name: word("register"), code: word("x"), qualified: word(`AgentStore(\.|#|::)register`),
		container: word("AgentStore"), containerID: 7, plain: true}
	for line, want := range map[string][2]bool{ // far from the container, near it
		"AgentStore.register creates an agent": {true, true},
		"see AgentStore#register":              {true, true},
		"agents register themselves":           {false, true},
		"registering takes a token":            {false, false},
		"the registerAgent route":              {false, false},
	} {
		if got := [2]bool{member.line(line, false), member.line(line, true)}; got != want {
			t.Errorf("%q: %v, want %v", line, got, want)
		}
	}
	// A top-level name that is an everyday word counts only as code.
	plain := mention{name: word("list"), code: mentionCode("list"), plain: true}
	distinct := mention{name: word("hashToken"), code: mentionCode("hashToken")}
	for line, want := range map[string][2]bool{ // plain, distinct
		"the list of agents":               {false, false},
		"call list() first":                {true, false},
		"`list` returns them":              {true, false},
		"hashToken returns the hex digest": {false, true},
		"rehashTokens is something else":   {false, false},
	} {
		if got := [2]bool{plain.line(line, false), distinct.line(line, false)}; got != want {
			t.Errorf("%q: %v, want %v", line, got, want)
		}
	}
}

func TestSignatureTail(t *testing.T) {
	for _, c := range []struct{ name, sig, want string }{
		{"New", "func New(name string) *Client", "(name string) *Client"},
		{"string", "export declare function string<T>(params?: P): Z<T>;", "<T>(params?: P): Z<T>"},
		{"ping", "ping(): Promise<{", "(): Promise<{ ..."},
		{"oninit", "oninit?: () => void;", "?: () => void"},
		{"Close", "Shut() error", " Shut() error"},
	} {
		if got := signatureTail(store.Symbol{Name: c.name, Signature: c.sig}); got != c.want {
			t.Errorf("%s: %q, want %q", c.sig, got, c.want)
		}
	}
}

func TestDepStateLines(t *testing.T) {
	u := store.DepUse{Eco: "npm", Name: "x", Version: "1.0.0", Manifest: "package.json", Error: "boom"}
	for state, want := range map[string]string{
		store.DepUnused: "x@1.0.0 is declared in package.json but no source file imports it, so it is not indexed; " +
			"name it under deps.include in the codedoc config and run codedoc deps index",
		store.DepMissing: "x@1.0.0 is declared in package.json but not installed; install the dependencies and run codedoc deps index",
		store.DepFailed:  "indexing x@1.0.0 failed: boom; codedoc deps index tries again",
		store.DepSkipped: "x@1.0.0 is a package of the workspace, indexed as a repo: codedoc outline and codedoc find answer for it",
		store.DepPending: "x@1.0.0 is declared in package.json and waits to be indexed; run codedoc deps index",
	} {
		u.State = state
		if got := depStateLine(u); got != want {
			t.Errorf("%s: %q", state, got)
		}
	}
}

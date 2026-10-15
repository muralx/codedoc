package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// list returns n entries named <prefix>0.. with a text of 20 bytes.
func list(prefix string, n int) []Entry {
	out := make([]Entry, n)
	for i := range out {
		out[i] = Entry{Key: fmt.Sprintf("%s%d", prefix, i), Text: strings.Repeat("x", 20)}
	}
	return out
}

func textOf(t *testing.T, r *Response, budget int) string {
	t.Helper()
	var b bytes.Buffer
	if _, err := Render(&b, r, "text", budget); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// A section with a room is cut to it, names its own command for the
// rest, and leaves the sections after it their place.
func TestSectionRoomCutsOneSectionAndKeepsTheNext(t *testing.T) {
	r := &Response{Bare: true, Command: "codedoc impact X", Sections: []Section{
		{Title: "callers", Entries: list("a", 30), Room: 120, Command: "codedoc callers X"},
		{Title: "tests", Entries: list("b", 2), Room: 80},
	}}
	got := textOf(t, r, 400)
	want := "== callers (30)\na0  xxxxxxxxxxxxxxxxxxxx\na1  xxxxxxxxxxxxxxxxxxxx\n28 more: codedoc callers X --offset 2\n" +
		"== tests (2)\nb0  xxxxxxxxxxxxxxxxxxxx\nb1  xxxxxxxxxxxxxxxxxxxx\n"
	if got != want {
		t.Fatalf("got\n%s", got)
	}
	// Without rooms the first section takes the budget and the second is
	// only counted, with the command that prints it.
	r.Sections[0].Room, r.Sections[1].Room = 0, 0
	if got := textOf(t, r, 400); !strings.HasSuffix(got, "... 1 more section (tests): codedoc impact X --budget 1300\n") {
		t.Fatalf("got\n%s", got)
	}
}

// What a section leaves of its room goes to the sections after it, and
// a section that fits its room exactly is whole: no room is kept for a
// more line it does not need.
func TestSectionRoomSpareAndExactFit(t *testing.T) {
	whole := Section{Title: "few", Entries: list("a", 3)}
	size := SectionsSize([]Section{whole})
	whole.Room = size
	r := &Response{Bare: true, Sections: []Section{whole, {Title: "many", Entries: list("b", 30), Room: 70, Command: "more"}}}
	if got := textOf(t, r, 2000); !strings.HasPrefix(got, "== few (3)\na0  xxxxxxxxxxxxxxxxxxxx\na1  xxxxxxxxxxxxxxxxxxxx\n"+
		"a2  xxxxxxxxxxxxxxxxxxxx\n== many (30)\nb0  xxxxxxxxxxxxxxxxxxxx\n29 more: more --offset 1\n") {
		t.Fatalf("got\n%s", got)
	}
	// With 100 bytes to spare from the first section, the second lists
	// more than its own room holds.
	r.Sections[0].Room = size + 100
	if got := textOf(t, r, 2000); !strings.Contains(got, "\nb3  xxxxxxxxxxxxxxxxxxxx\n") {
		t.Fatalf("spare room not handed on:\n%s", got)
	}
	if sizes := SectionSizes(r); sizes[0] != size || sizes[1] <= 70 {
		t.Fatalf("sizes ignore the rooms: %v", sizes)
	}
}

// A section too small for one entry names its command, without an
// offset that would say nothing.
func TestSectionCommandWhenNothingFits(t *testing.T) {
	r := &Response{Bare: true, Sections: []Section{
		{Title: "callers", Entries: []Entry{{Key: "a", Text: strings.Repeat("x", 300)}}, Room: 30, Command: "codedoc callers X"},
		{Title: "tests", Entries: list("b", 1), Room: 60},
	}}
	if got := textOf(t, r, 1000); got != "== callers (1)\n1 more: codedoc callers X\n== tests (1)\nb0  xxxxxxxxxxxxxxxxxxxx\n" {
		t.Fatalf("got\n%s", got)
	}
}

// An entry that stands for several counts as that many in the title, in
// the omission line and in the offset that continues the section, and
// prints its More line after its fragments, indented as itself.
func TestEntryCoversAndMore(t *testing.T) {
	entries := list("a", 8)
	entries[1].Covers, entries[1].More = 5, "4 more in this file: codedoc callers X --offset 2"
	entries[1].Depth = 1
	entries[1].Fragments = []Fragment{{Start: 7, End: 7, Text: "call()\n"}}
	sec := Section{Title: "callers", Entries: entries, Command: "codedoc callers X"}
	r := &Response{Bare: true, Sections: []Section{sec}}
	got := textOf(t, r, 0)
	if !strings.HasPrefix(got, "== callers (12)\n") ||
		!strings.Contains(got, "  a1  xxxxxxxxxxxxxxxxxxxx\n7\tcall()\n  4 more in this file: codedoc callers X --offset 2\na2  ") {
		t.Fatalf("got\n%s", got)
	}
	// Cut after the third line: seven ranked entries are listed or stood
	// for, five are left.
	r.Sections[0].Room = SectionsSize([]Section{{Title: "callers", Entries: entries[:3]}}) + 45
	if got := textOf(t, r, 4000); !strings.HasSuffix(got, "a2  xxxxxxxxxxxxxxxxxxxx\n5 more: codedoc callers X --offset 7\n") {
		t.Fatalf("got\n%s", got)
	}
}

// JSON carries what an entry stands for and counts it among the omitted.
func TestEntryCoversInJSON(t *testing.T) {
	entries := list("a", 8)
	entries[1].Covers, entries[1].More = 5, "4 more in this file: codedoc callers X --offset 2"
	r := &Response{Bare: true, Sections: []Section{{Title: "callers", Entries: entries}}}
	var b bytes.Buffer
	if _, err := Render(&b, r, "json", 0); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Sections []struct {
			Entries []struct {
				Covers int    `json:"covers"`
				More   string `json:"more"`
			} `json:"entries"`
			Omitted int `json:"omitted"`
		} `json:"sections"`
	}
	if err := json.Unmarshal(b.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if e := out.Sections[0].Entries[1]; e.Covers != 5 || e.More == "" || out.Sections[0].Omitted != 0 {
		t.Fatalf("json: %s", b.String())
	}
	// Under a budget JSON counts the covered entries among the omitted.
	b.Reset()
	if _, err := Render(&b, r, "json", 200); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), `"omitted":11,"next_offset":1`) {
		t.Fatalf("json under budget: %s", b.String())
	}
}

// One symbol listed at several places of a call tree prints the same
// line each time: no part of its key would tell the lines apart.
func TestSameSymbolTwiceKeepsItsName(t *testing.T) {
	e := Entry{Key: "r:go:m:leaf", Name: "leaf", Loc: "a.go:3", Depth: 1}
	other := Entry{Key: "r:go:n:leaf", Name: "leaf", Loc: "a.go:3", Depth: 1}
	r := &Response{Bare: true, Sections: []Section{{Title: "flow", Entries: []Entry{{Key: "r:go:m:root", Name: "root"}, e, e}}}}
	if got := textOf(t, r, 0); got != "== flow (3)\nroot\n  leaf  a.go:3\n  leaf  a.go:3\n" {
		t.Fatalf("got\n%s", got)
	}
	r.Sections[0].Entries[2] = other
	if got := textOf(t, r, 0); got != "== flow (3)\nroot\n  m:leaf  a.go:3\n  n:leaf  a.go:3\n" {
		t.Fatalf("two symbols must be told apart:\n%s", got)
	}
}

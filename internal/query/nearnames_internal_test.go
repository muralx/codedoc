package query

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/store"
)

func TestEditDistanceIsBounded(t *testing.T) {
	for _, c := range []struct {
		a, b  string
		limit int
		want  int
	}{
		{"register", "register", 2, 0},
		{"regsiter", "register", 2, 2}, // a transposition is two edits
		{"registr", "register", 2, 1},
		{"register", "registry", 2, 2},
		{"", "abc", 3, 3},
		{"abc", "", 2, 3},              // the lengths alone exceed the limit
		{"kitten", "sitting", 2, 3},    // three edits: over the limit
		{"abcdefgh", "zzzzzzzz", 1, 2}, // given up as soon as a row is over
	} {
		if got := editDistance(c.a, c.b, c.limit); got != c.want {
			t.Errorf("editDistance(%q, %q, %d) = %d, want %d", c.a, c.b, c.limit, got, c.want)
		}
	}
	for n, want := range map[int]int{1: 1, 4: 1, 8: 2, 12: 3, 40: 3} {
		if got := typoLimit(n); got != want {
			t.Errorf("typoLimit(%d) = %d, want %d", n, got, want)
		}
	}
}

func TestRankNamesOrdersByNearness(t *testing.T) {
	names := []string{"register", "Register", "unregister", "registerAgent", "regsiter", "registry", "reg", "session", "id"}
	var got []string
	for _, n := range rankNames(names, "REGISTER") {
		got = append(got, fmt.Sprintf("%s:%d", n.name, n.tier))
	}
	want := []string{"Register:0", "register:0", "unregister:1", "registerAgent:2", "registry:4", "regsiter:4"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
	// A short name is near only what equals it or is a typo away: it is
	// a part of too much.
	if got := rankNames([]string{"id", "idle", "valid", "ID", "is"}, "id"); len(got) != 3 || got[0].name != "ID" || got[2].tier != nearTypo {
		t.Errorf("short name: %+v", got)
	}
	// A name of the index that is part of the one asked for.
	if got := rankNames([]string{"create", "new"}, "createProgram"); len(got) != 1 || got[0].tier != nearPart {
		t.Errorf("part of the address: %+v", got)
	}
}

func TestIdentWords(t *testing.T) {
	for in, want := range map[string]string{
		"registerTool":     "register tool",
		"HOOK_HUB_START":   "hook hub start",
		"McpToolListing":   "mcp tool listing",
		"test:it works ok": "test it works ok",
		"x2Y":              "x2y",
	} {
		if got := strings.Join(identWords(in), " "); got != want {
			t.Errorf("identWords(%q) = %q, want %q", in, got, want)
		}
	}
	if !sharesWord("registerTool", "ToolContext") || sharesWord("registerTool", "summarise") || sharesWord("runIt", "getIt") {
		t.Error("sharesWord: a word of four letters or more in common")
	}
}

func TestShownNamesCountsTheRest(t *testing.T) {
	if got := shownNames([]string{"a", "b", "c"}, 3); got != "a, b, c" {
		t.Error(got)
	}
	if got := shownNames([]string{"a", "b", "c", "d", "e"}, 3); got != "a, b, c, +2 more" {
		t.Error(got)
	}
}

func TestUsageErrorsAreErrUsage(t *testing.T) {
	err := usagef("line range %q: want A-B", "x")
	if !errors.Is(err, ErrUsage) || err.Error() != `line range "x": want A-B` || errors.Is(err, ErrNotFound) {
		t.Errorf("%v", err)
	}
	if !errors.Is(ErrNearest, ErrNotFound) || errors.Is(ErrNotIndexed, ErrNotFound) {
		t.Error("the nearest candidates are a not found; a path outside the index is its own error")
	}
}

func TestFilePartOfAnAddress(t *testing.T) {
	for addr, want := range map[string]filePart{
		"a/b.ts#Name":   {target: "a/b.ts", name: "Name", typos: true},
		"r:a/b.ts#Name": {target: "r:a/b.ts", name: "Name", typos: true},
		"a/b.ts:12":     {target: "a/b.ts", line: 12, typos: true},
		"a/b.ts":        {target: "a/b.ts"},
		"r:a/b":         {target: "r:a/b"},
	} {
		if got, ok := filePartOf(ParseAddr(addr)); !ok || got != want {
			t.Errorf("%s: %+v, %v", addr, got, ok)
		}
	}
	for _, addr := range []string{"Name", "Store.Get", "r:go:pkg:Name"} {
		if got, ok := filePartOf(ParseAddr(addr)); ok {
			t.Errorf("%s has no file part: %+v", addr, got)
		}
	}
}

func TestDepStateReasonPerState(t *testing.T) {
	for state, want := range map[string]string{
		store.DepUnused:  "no source file imports the dependency left-pad; name it under deps.include in the config to index it",
		store.DepPending: "the dependency left-pad waits to be indexed; run codedoc deps index",
		store.DepFailed:  "indexing the dependency left-pad failed: no entry point",
		store.DepMissing: "the dependency left-pad is not installed",
		store.DepSkipped: "",
		store.DepIndexed: "of the dependency left-pad only the declarations its entry points export are indexed",
	} {
		if got := depStateReason(store.DepUse{Name: "left-pad", State: state, Error: "no entry point"}); got != want {
			t.Errorf("%s: %q", state, got)
		}
	}
}

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/index"
)

// TestFreshRemarkSaysHowTheIndexCatchesUp checks the remark of a repo
// line: nothing when fresh, else why the index is behind and what brings
// it up to date, which over the query limit is only codedoc index.
func TestFreshRemarkSaysHowTheIndexCatchesUp(t *testing.T) {
	const next = "; the next query or codedoc index brings it up to date)"
	cases := []struct {
		f    index.Freshness
		want string
	}{
		{index.Freshness{Fresh: true}, ""},
		{index.Freshness{}, " (index behind: the head moved or files were removed since indexed" + next},
		{index.Freshness{Changed: 1}, " (index behind: 1 file changed since indexed" + next},
		{index.Freshness{Changed: 9, Outdated: 9}, " (index behind: extractor updated, 9 files to re-extract" + next},
		{index.Freshness{Changed: 201}, " (index behind: 201 files changed since indexed; run codedoc index to bring it up to date)"},
	}
	for _, c := range cases {
		if got := freshRemark(c.f, 200); got != c.want {
			t.Errorf("%+v: %q, want %q", c.f, got, c.want)
		}
	}
}

// TestStatusSaysABehindIndexCatchesUp checks that status, after files
// changed, words the repo line as an index that is behind and will catch
// up, keeps the JSON header fields, and that the next query does catch
// up.
func TestStatusSaysABehindIndexCatchesUp(t *testing.T) {
	_, goLib := setupWorkspace(t)
	if _, err := runCLI(t, "index", "--workspace", "fx"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(goLib, "store", "null.go")); err != nil {
		t.Fatal(err)
	}
	out, err := runCLI(t, "status", "--workspace", "fx", "--repo", "go-lib")
	if err != nil || !strings.Contains(out, "scip:never (index behind: the head moved or files were removed since indexed; "+
		"the next query or codedoc index brings it up to date)\n") {
		t.Fatalf("%v\n%s", err, out)
	}
	checkBehindJSON(t)
	if _, err := runCLI(t, "outline", "store", "--workspace", "fx", "--repo", "go-lib"); err != nil {
		t.Fatal(err)
	}
	out, err = runCLI(t, "status", "--workspace", "fx", "--repo", "go-lib")
	if err != nil || strings.Contains(out, "index behind") || strings.Contains(out, "fresh:no") {
		t.Fatalf("a query did not bring the index up to date: %v\n%s", err, out)
	}
}

// checkBehindJSON checks the JSON status of go-lib, whose index is
// behind, keeps its header fields.
func checkBehindJSON(t *testing.T) {
	t.Helper()
	out, err := runCLI(t, "status", "--workspace", "fx", "--repo", "go-lib", "--format", "json")
	var resp struct {
		Header map[string]any `json:"header"`
	}
	if err != nil || json.Unmarshal([]byte(out), &resp) != nil || resp.Header["fresh"] != false || resp.Header["repo"] != "go-lib" {
		t.Fatalf("JSON header changed: %v\n%s", err, out)
	}
}

// TestStatusRejectsABadFreshLimit checks status reports an unusable
// CODEDOC_FRESH_MAX_FILES the way queries do.
func TestStatusRejectsABadFreshLimit(t *testing.T) {
	setupWorkspace(t)
	if _, err := runCLI(t, "index", "--workspace", "fx"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEDOC_FRESH_MAX_FILES", "many")
	if _, err := runCLI(t, "status", "--workspace", "fx"); err == nil || !strings.Contains(err.Error(), "CODEDOC_FRESH_MAX_FILES") {
		t.Fatalf("err = %v", err)
	}
}

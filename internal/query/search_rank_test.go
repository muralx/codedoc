package query_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/embed"
	"github.com/muralx/codedoc/internal/query"
	"github.com/muralx/codedoc/internal/testutil"
)

// A word that occurs only inside a function body, as a name it uses,
// finds the function, with the lines of the uses as its passages.
func TestSearchFindsANameUsedOnlyInABody(t *testing.T) {
	c := workspaceOf(t, "go-lib")
	r := search(t, c, "fallback", query.SearchOpts{})
	got := keys(r)
	if len(got) != 2 || got[0] != libService+"fallback" || got[1] != libService+"Service.Lookup" {
		t.Fatalf("%v", got)
	}
	// Lookup calls fallback on line 20 of service/service.go.
	if p := passagesOf(t, r, ":Service.Lookup"); len(p) != 1 || p[0] != (query.Passage{StartLine: 20, EndLine: 20}) {
		t.Fatalf("%+v", p)
	}
	if r := search(t, c, "fallback", query.SearchOpts{Kind: "module"}); len(r.Sections[0].Entries) != 0 {
		t.Fatalf("a module search looks into no body:\n%s", render(t, r))
	}
}

// A query that reads like a behaviour finds the code that does it among
// the first two hits, whatever number of sections of one document
// mention it, and the total counts what matched, not what the lists
// returned.
func TestSearchRanksCodeAmongTheSectionsThatMentionIt(t *testing.T) {
	root := testutil.CopyFixture(t, "go-lib")
	var notes strings.Builder
	notes.WriteString("# Zoo\n\nThe keepers feed every animal.\n")
	for _, part := range []string{"Morning", "Noon", "Evening"} {
		fmt.Fprintf(&notes, "\n## %s\n\nIn the %s a keeper may feed the ferret, if the ferret is awake.\n", part, strings.ToLower(part))
	}
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs/zoo.md"), []byte(notes.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	code := "package store\n\n// feedFerret feeds the ferret its dinner.\nfunc feedFerret() {}\n"
	if err := os.WriteFile(filepath.Join(root, "store/zoo.go"), []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}
	c := indexRepos(t, &config.Repo{Name: "go-lib", Path: root})
	c.Embedder = embed.NewFake(4096)
	r := search(t, c, "feed the ferret", query.SearchOpts{})
	got := keys(r)
	// One section may match as well as the function; the next one of the
	// same document does not come before it.
	if len(got) < 4 || !slices.ContainsFunc(got[:2], func(k string) bool { return strings.HasSuffix(k, ":feedFerret") }) {
		t.Fatalf("%v", got)
	}
	if total := r.Sections[0].Total; total > 8 {
		t.Fatalf("%d hits for a query three sections and one function match:\n%s", total, render(t, r))
	}
}

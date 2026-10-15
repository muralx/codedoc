package index_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/store"
	"github.com/muralx/codedoc/internal/testutil"
)

// docSymbols renders the symbols of a file as "kind key lines state".
func docSymbols(t *testing.T, s *store.Store, root, rel string) []string {
	t.Helper()
	syms, err := s.SymbolsInFile(fileRow(t, s, root, rel).ID)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(syms))
	for i, sy := range syms {
		out[i] = fmt.Sprintf("%s %s %d-%d %s", sy.Kind, sy.StableKey, sy.StartLine, sy.EndLine, sy.DocState)
	}
	return out
}

// linkEdges renders the link edges of the store as "src -> dst".
func linkEdges(t *testing.T, s *store.Store, root string) []string {
	t.Helper()
	r, err := s.RepoByPath(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := s.FilesByRepo(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range files {
		syms, _ := s.SymbolsInFile(f.ID)
		for _, sy := range syms {
			edges, err := s.EdgesTo(sy.ID, "references")
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range edges {
				src, _ := s.SymbolByID(e.Src)
				out = append(out, fmt.Sprintf("%s:%d %q -> %s %q [%s]", src.Path, e.Line, src.Qualified, sy.Path,
					sy.Qualified, e.RefKind))
			}
		}
	}
	sort.Strings(out)
	return out
}

func sameLines(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestDocumentsAreIndexedAsSections(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "doc-repo")
	indexRepo(t, s, rep)
	got := docSymbols(t, s, rep.Path, "docs/runbooks/retries.md")
	sameLines(t, got, []string{
		"document doc-repo:doc:docs/runbooks/retries.md: 1-12 ok",
		"section doc-repo:doc:docs/runbooks/retries.md:Retry runbook 1-12 ok",
		"section doc-repo:doc:docs/runbooks/retries.md:Retry runbook > Backoff 6-8 ok",
		"section doc-repo:doc:docs/runbooks/retries.md:Retry runbook > Escalation 10-12 ok",
	})
	if f := fileRow(t, s, rep.Path, "docs/design.md"); f.Lang != "markdown" || f.Title != "Store design" {
		t.Errorf("design.md row = %+v", f)
	}
	if f := fileRow(t, s, rep.Path, "docs/CHANGES.txt"); f.Lang != "text" || f.Title != "" {
		t.Errorf("CHANGES.txt row = %+v", f)
	}
	for rel, n := range map[string]int{"docs/notes.rst": 6, "docs/guide.adoc": 6, "docs/CHANGES.txt": 1, "settings.json": 0} {
		if got := docSymbols(t, s, rep.Path, rel); len(got) != n {
			t.Errorf("%s: %d symbols, want %d: %v", rel, len(got), n, got)
		}
	}
}

func TestSectionTextIsSearchable(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "doc-repo")
	indexRepo(t, s, rep)
	names := func(match string) []string {
		ids, err := s.SectionMatches(match, store.SearchFilter{}, 10)
		if err != nil {
			t.Fatal(err)
		}
		syms, _ := s.SymbolsByID(ids)
		var out []string
		for _, id := range ids {
			out = append(out, syms[id].Path+"#"+syms[id].Qualified)
		}
		return out
	}
	sameLines(t, names("exponentially"), []string{"docs/design.md#6. Delivery > 6.2 Retries > Notes"})
	sameLines(t, names("compressed"), []string{"docs/design.md#Open questions"})
	if got := names(`"shell comment"`); len(got) != 1 || got[0] != "docs/design.md#2. Model > 2.1 Keys" {
		t.Errorf("fenced text belongs to its section: %v", got)
	}
	editFile(t, rep.Path, "docs/design.md", "Should values be compressed?", "Should values be encrypted?")
	indexRepo(t, s, rep)
	if got := names("compressed"); len(got) != 0 {
		t.Errorf("stale section text: %v", got)
	}
	sameLines(t, names("encrypted"), []string{"docs/design.md#Open questions"})
	if err := os.Remove(filepath.Join(rep.Path, "docs/design.md")); err != nil {
		t.Fatal(err)
	}
	indexRepo(t, s, rep)
	if got := names("encrypted OR exponentially"); len(got) != 0 {
		t.Errorf("text of a deleted document: %v", got)
	}
}

func TestDocumentLinksBecomeReferenceEdges(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "doc-repo")
	indexRepo(t, s, rep)
	want := []string{
		`docs/design.md:49 "6. Delivery > 6.2 Retries" -> docs/runbooks/retries.md "Retry runbook > Backoff" [link]`,
		`docs/design.md:7 "" -> docs/guide.adoc "" [link]`,
		`docs/design.md:8 "" -> docs/notes.rst "" [link]`,
		`docs/runbooks/retries.md:4 "Retry runbook" -> docs/design.md "6. Delivery" [link]`,
	}
	sameLines(t, linkEdges(t, s, rep.Path), want)
	// A target that goes away takes its edges along; one that comes
	// back gets them again though the linking document did not change.
	guide, err := os.ReadFile(filepath.Join(rep.Path, "docs/guide.adoc"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(rep.Path, "docs/guide.adoc")); err != nil {
		t.Fatal(err)
	}
	indexRepo(t, s, rep)
	sameLines(t, linkEdges(t, s, rep.Path), append([]string{want[0]}, want[2:]...))
	writeFile(t, rep.Path, "docs/guide.adoc", guide)
	indexRepo(t, s, rep)
	sameLines(t, linkEdges(t, s, rep.Path), want)
	// A renamed heading no longer matches the anchor: the link falls
	// back to the document.
	editFile(t, rep.Path, "docs/runbooks/retries.md", "## Backoff", "## Waiting")
	indexRepo(t, s, rep)
	if got := linkEdges(t, s, rep.Path); !strings.Contains(got[0], `-> docs/runbooks/retries.md "" [link]`) {
		t.Errorf("after the rename: %v", got)
	}
}

func TestDocumentKeysSurviveReindex(t *testing.T) {
	s := openTemp(t)
	root := testutil.SourceRepo(t, "kd", map[string]string{"a.md": "# Issue #1234\n\ntext\n\n# Issue #abcd\n",
		"b.md": "# Issue #1234\n"})
	rep := &config.Repo{Path: root, Name: "kd"}
	indexRepo(t, s, rep)
	want := []string{"document kd:doc:a.md: 1-5 ok", "section kd:doc:a.md:Issue #1234 1-3 ok",
		"section kd:doc:a.md:Issue #abcd 5-5 ok"}
	sameLines(t, docSymbols(t, s, root, "a.md"), want)
	before, _ := s.SymbolByKey("kd:doc:a.md:Issue #1234")
	editFile(t, root, "a.md", "text", "more text")
	if err := os.Remove(filepath.Join(root, "b.md")); err != nil {
		t.Fatal(err)
	}
	indexRepo(t, s, rep)
	sameLines(t, docSymbols(t, s, root, "a.md"), want)
	if after, _ := s.SymbolByKey("kd:doc:a.md:Issue #1234"); after.ID != before.ID || after.BodyHash == before.BodyHash {
		t.Errorf("a kept section keeps its row: %+v -> %+v", before, after)
	}
}

func TestLargeDocumentIsListedNotSectioned(t *testing.T) {
	s := openTemp(t)
	big := "# Big\n\n" + strings.Repeat("line of text\n", 200000)
	root := testutil.SourceRepo(t, "big", map[string]string{"big.md": big, "small.md": "# Small\n",
		"big.go": "package big\n\n// " + strings.Repeat("x", 3<<20) + "\n", "bin.txt": "a\x00" + big})
	indexRepo(t, s, &config.Repo{Path: root, Name: "big"})
	f := fileRow(t, s, root, "big.md")
	if f.Lines != 200002 || f.Size != int64(len(big)) || f.Hash != index.ContentHash([]byte(big)) || f.Title != "" {
		t.Errorf("big.md row = lines %d size %d hash %s title %q", f.Lines, f.Size, f.Hash, f.Title)
	}
	if got := docSymbols(t, s, root, "big.md"); len(got) != 0 {
		t.Errorf("a large document has no sections: %v", got[:1])
	}
	r, _ := s.RepoByPath(root)
	for _, rel := range []string{"big.go", "bin.txt"} {
		if _, err := s.FileByPath(r.ID, rel); err == nil {
			t.Errorf("%s is indexed", rel)
		}
	}
	found, _, err := index.Discover(root, nil, map[string]bool{"go": true})
	if err != nil || len(found) != 0 {
		t.Errorf("languages: [go] keeps documents out: %v %v", found, err)
	}
}

func TestCodeFileTitleIsItsModuleDoc(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	indexRepo(t, s, rep)
	if f := fileRow(t, s, rep.Path, "store/doc.go"); f.Title != "Package store persists the fixture's key-value data." {
		t.Errorf("doc.go title = %q", f.Title)
	}
	if f := fileRow(t, s, rep.Path, "store/null.go"); f.Title != "" {
		t.Errorf("null.go title = %q", f.Title)
	}
}

func TestSectionsAreEmbedded(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "doc-repo")
	indexRepo(t, s, rep)
	e := newCounting()
	res := embedAll(t, s, e)
	n, documented, err := s.SymbolVecCounts(store.VecScope{Bodies: true}, 16)
	if err != nil || n != documented || n != res.Symbols || n != 29 {
		t.Fatalf("%+v %d/%d %v", res, n, documented, err)
	}
	want := "docs/design.md\n6. Delivery > 6.2 Retries\nA failed delivery is retried three times. Details are in the\n" +
		"[retry runbook](runbooks/retries.md#backoff)."
	found := false
	for _, text := range e.texts {
		found = found || strings.HasSuffix(text, want)
	}
	if !found {
		t.Errorf("no embedding text %q in\n%s", want, strings.Join(e.texts, "\n--\n"))
	}
	if n, err := s.DeleteUnembeddedVecs(0); err != nil || n != 0 {
		t.Errorf("sections without text keep their vectors: %d %v", n, err)
	}
}

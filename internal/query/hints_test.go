package query_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/query"
)

// continueHint matches the command a cut body ends with.
var continueHint = regexp.MustCompile(`(?m)^(\d+) more lines: codedoc lines (\S+|'[^']*') (\d+)-(\d+)$`)

// numbered matches a body line: its number, a tab and the text.
var numbered = regexp.MustCompile(`(?m)^(\d+)\t(.*)$`)

// followLines pastes the continue command of text again and again, from
// the working directory and repo of c, until an answer has none. It
// returns the body lines of every answer by line number, failing when
// a line is printed twice or an answer exceeds the budget.
func followLines(t *testing.T, c *query.Ctx, text string, budget int) map[int]string {
	t.Helper()
	got := map[int]string{}
	for range 10000 {
		if len(text) > budget {
			t.Fatalf("%d bytes under --budget %d:\n%s", len(text), budget, text)
		}
		for _, m := range numbered.FindAllStringSubmatch(text, -1) {
			n, _ := strconv.Atoi(m[1])
			if _, dup := got[n]; dup {
				t.Fatalf("line %d printed twice:\n%s", n, text)
			}
			got[n] = m[2]
		}
		m := continueHint.FindStringSubmatch(text)
		if m == nil {
			if strings.Contains(text, "body omitted") {
				t.Fatalf("an answer without lines and without a continue command:\n%s", text)
			}
			return got
		}
		resp, err := query.Lines(c, strings.Trim(m[2], "'"), m[3]+"-"+m[4])
		if err != nil {
			t.Fatalf("pasting %q: %v", m[0], err)
		}
		text = budgeted(t, resp, budget)
	}
	t.Fatal("the continue commands never end")
	return nil
}

func TestContinueHintsLeadToTheEndOfALongSection(t *testing.T) {
	c := bigDocWorkspace(t, map[string]string{"docs/with space & more.md": bigDoc()})
	src := strings.Split(strings.TrimSuffix(bigDoc(), "\n"), "\n")
	for _, file := range []string{"docs/big.md", "docs/with space & more.md"} {
		for _, budget := range []int{600, 2000, 8000} {
			// The working directory is not the repo: the hints must name
			// the file so that they work from anywhere.
			c.Budget, c.Repo, c.Cwd = budget, nil, t.TempDir()
			resp, err := query.Symbol(c, "big:"+file+"#Huge", query.SymbolOpts{})
			if err != nil {
				t.Fatal(err)
			}
			first := budgeted(t, resp, budget)
			if !continueHint.MatchString(first) {
				t.Fatalf("%s budget %d: the section is not cut:\n%s", file, budget, first)
			}
			got := followLines(t, c, first, budget)
			// The section "## Huge" is the lines 3 to 304 of the file.
			if len(got) != 302 {
				t.Fatalf("%s budget %d: %d lines, want 302", file, budget, len(got))
			}
			for n := 3; n <= 304; n++ {
				if got[n] != src[n-1] {
					t.Fatalf("%s budget %d: line %d = %q, want %q", file, budget, n, got[n], src[n-1])
				}
			}
		}
	}
}

func TestLinesPrintsWhatFitsAndContinues(t *testing.T) {
	c := bigDocWorkspace(t, nil)
	c.Budget = 1000
	text := budgeted(t, must(query.Lines(c, "big:docs/big.md", "10-308")).resp, 1000)
	mustContain(t, text, "docs/big.md:10-308  in Big (1-308)\n10\tline 5 of the huge section",
		" more lines: codedoc lines big:docs/big.md ")
	if strings.Contains(text, "body omitted") || len(text) > 1000 {
		t.Fatalf("%d bytes:\n%s", len(text), text)
	}
	got := followLines(t, c, text, 1000)
	src := strings.Split(strings.TrimSuffix(bigDoc(), "\n"), "\n")
	for n := 10; n <= 308; n++ {
		if got[n] != src[n-1] {
			t.Fatalf("line %d = %q, want %q", n, got[n], src[n-1])
		}
	}
}

func TestContinueHintOfADependencyFileWorksFromAnywhere(t *testing.T) {
	c := depsWorkspace(t)
	root := c.Workspace.Repos[0].Path
	src, err := os.ReadFile(filepath.Join(root, sdkServerFile))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Split(strings.TrimSuffix(string(src), "\n"), "\n")
	c.Budget = 600
	text := budgeted(t, must(query.Lines(c, "deps-app:"+sdkServerFile, "1-"+strconv.Itoa(len(want)))).resp, 600)
	if !continueHint.MatchString(text) {
		t.Fatalf("not cut:\n%s", text)
	}
	c.Cwd = t.TempDir()
	got := followLines(t, c, text, 600)
	for i, l := range want {
		if got[i+1] != l {
			t.Fatalf("line %d = %q, want %q", i+1, got[i+1], l)
		}
	}
}

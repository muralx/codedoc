package query

import (
	"slices"
	"strings"
	"unicode"

	"github.com/muralx/codedoc/internal/output"
)

// textHitCap is the most full-text hits offered for a name nothing is
// near.
const textHitCap = 5

// textHits are the candidates of a name no symbol name is near: the
// full-text hits that hold every word of the name as a whole word (a
// hit sharing one of several words is no reading of the name), best
// first, textHitCap at most, without the lines that matched. The search
// is text-only (no model is loaded to answer a mistyped name) and its
// notes are dropped.
func (c *Ctx) textHits(name string) []output.Entry {
	want := words(name)
	if len(want) == 0 {
		return nil
	}
	embedder, offset, notes := c.Embedder, c.Offset, c.notes
	c.Embedder, c.Offset = nil, 0
	resp, err := Search(c, name, SearchOpts{Limit: nearestCap})
	c.Embedder, c.Offset, c.notes = embedder, offset, notes
	if err != nil || len(resp.Sections) == 0 {
		return nil
	}
	var entries []output.Entry
	for _, e := range resp.Sections[0].Entries {
		if len(entries) == textHitCap {
			break
		}
		if !holdsAll(e, want) {
			continue
		}
		if resp.Header.Workspace != "" {
			e.RepoID = 0 // the search already put the repo in the location
		}
		e.Fragments = nil // a candidate is a line: its name and where it is
		entries = append(entries, e)
	}
	return entries
}

// words splits a text into its lower-case words: runs of letters,
// digits and underscores.
func words(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	})
}

// holdsAll reports whether a search hit holds every one of the words as
// a whole word: in its name, its signature, its doc sentence or the
// lines that matched.
func holdsAll(e output.Entry, want []string) bool {
	text := []string{e.Name, e.Text, e.Doc}
	for _, f := range e.Fragments {
		text = append(text, f.Text)
	}
	have := words(strings.Join(text, " "))
	for _, w := range want {
		if !slices.Contains(have, w) {
			return false
		}
	}
	return true
}

package query

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/muralx/codedoc/internal/docs"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/stablekey"
	"github.com/muralx/codedoc/internal/store"
)

// symbolEntry renders a symbol as one result line: stable key, path:line,
// signature, first sentence of the doc.
func symbolEntry(sy store.Symbol) output.Entry {
	e := output.Entry{Key: sy.StableKey, Name: shortName(sy), Loc: spanLoc(sy.Path, sy.StartLine, sy.EndLine), Text: sy.Signature,
		Doc: firstSentence(sy.DocText), Extra: map[string]any{"kind": sy.Kind}, RepoID: sy.RepoID}
	if isTestSymbol(sy) {
		e.Tags = append(e.Tags, testTag)
	}
	return e
}

// testTag marks an entry of test code.
const testTag = "test"

// isTestSymbol reports whether a symbol is test code: a test function
// (is_test) or any symbol of a test file by the language conventions
// gaps uses (docs.IsTestPath).
func isTestSymbol(sy store.Symbol) bool {
	return sy.IsTest || docs.IsTestPath(sy.Path)
}

// testsLast orders the entries tagged test after the others, keeping
// the order within each group, so production code leads a list that a
// budget cuts short.
func testsLast(entries []output.Entry) []output.Entry {
	slices.SortStableFunc(entries, func(a, b output.Entry) int {
		return cmp.Compare(boolRank(slices.Contains(a.Tags, testTag)), boolRank(slices.Contains(b.Tags, testTag)))
	})
	return entries
}

// isTestLoc reports whether a repo:path or path location is in a test
// file.
func isTestLoc(loc string) bool {
	if _, p, ok := strings.Cut(loc, ":"); ok {
		loc = p
	}
	return docs.IsTestPath(loc)
}

// boolRank is 1 for true and 0 for false, to sort false first.
func boolRank(b bool) int {
	if b {
		return 1
	}
	return 0
}

// spanLoc is the location of a line span: path:start, or path:start-end
// when it covers more than one line.
func spanLoc(p string, start, end int) string {
	if end <= start {
		return fmt.Sprintf("%s:%d", p, start)
	}
	return fmt.Sprintf("%s:%d-%d", p, start, end)
}

// docTags tags a symbol whose doc is missing or stale with its state
// (see docTag); an ok doc gets no tag.
func docTags(sy store.Symbol) []string {
	if sy.DocState == "missing" || sy.DocState == "stale" {
		return []string{docTag(sy.DocState)}
	}
	return nil
}

// detailed adds to the entries of a list what --detail says about the
// workspace symbol of each (syms, in the same order): its kind before
// its other tags, then its fan-in (the ambiguous edges that may end at
// it counted; one query for the whole list) and its doc state. A
// document or a section gets its kind alone. Without Ctx.Detail it does
// nothing.
func (c *Ctx) detailed(entries []output.Entry, syms []store.Symbol) error {
	if !c.Detail {
		return nil
	}
	ids := make([]int64, len(syms))
	for i, sy := range syms {
		ids[i] = sy.ID
	}
	fanIn, err := c.Store.FanIns(ids)
	if err != nil {
		return err
	}
	for i, sy := range syms {
		doc := docTag(sy.DocState)
		tags := []string{sy.Kind}
		for _, tag := range entries[i].Tags {
			if tag != doc {
				tags = append(tags, tag)
			}
		}
		if !isDocSymbol(sy) {
			tags = append(tags, fmt.Sprintf("fan-in:%d", fanIn[sy.ID]), doc)
		}
		entries[i].Tags = tags
	}
	return nil
}

// docTag is the tag of a doc state, the same in every answer:
// doc:ok, doc:stale or doc:missing.
func docTag(state string) string { return "doc:" + state }

// moduleOf is the module segment of a symbol's stable key.
func moduleOf(sy store.Symbol) string {
	k, err := stablekey.Parse(sy.StableKey)
	if err != nil {
		return ""
	}
	return k.Module
}

// firstSentence returns the first sentence of a doc on one line, whole
// however long it is; a doc without a sentence end gives its first
// paragraph.
func firstSentence(doc string) string {
	if i := strings.Index(doc, "\n\n"); i >= 0 {
		doc = doc[:i]
	}
	doc = strings.Join(strings.Fields(doc), " ")
	if i := strings.Index(doc, ". "); i >= 0 {
		doc = doc[:i+1]
	}
	return doc
}

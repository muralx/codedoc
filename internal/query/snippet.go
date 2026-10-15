package query

import (
	"strings"

	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/store"
)

// searchEntries renders fused hits in order: symbols as find does,
// modules as identity, path and language, each with the doc sentence
// that best matches the query words. Hits of the dependency database
// are tagged with their package version. An entry of a hit with
// passages lists them in Extra (see withPassages); that of a workspace
// symbol shows the lines that matched (see addFragments).
func (c *Ctx) searchEntries(hits []rankedHit, words []string) ([]output.Entry, error) {
	plain := make([]hit, len(hits))
	for i, h := range hits {
		plain[i] = h.hit
	}
	ws, err := loadRows(c.Store, plain, false)
	if err != nil {
		return nil, err
	}
	var dep rows
	if c.view != nil {
		if dep, err = loadRows(c.view.s, plain, true); err != nil {
			return nil, err
		}
	}
	out := make([]output.Entry, 0, len(hits))
	shown := shownLines{}
	for _, h := range hits {
		switch {
		case h.dep:
			if e, ok := c.view.searchEntry(dep, h.hit, words); ok {
				out = append(out, withPassages(e, h.passages))
			}
		case h.module:
			if m, ok := ws.mods[h.id]; ok {
				out = append(out, searchModuleEntry(m, words))
			}
		default:
			if sy, ok := ws.syms[h.id]; ok {
				e := symbolEntry(sy)
				e.Doc = snippet(sy.DocText, words)
				e.Tags = append(e.Tags, kindTags(sy)...)
				c.addFragments(&e, sy, h.passages, words)
				e.Fragments = shown.unseen(fileRef{sy.RepoID, sy.FileID}, e.Fragments)
				out = append(out, withPassages(e, h.passages))
			}
		}
	}
	return out, nil
}

// rows are the symbol and module rows of the hits of one store.
type rows struct {
	syms map[int64]store.Symbol
	mods map[int64]store.Module
}

// loadRows reads the rows of the hits that belong to a store: the
// dependency database with dep, else the workspace index.
func loadRows(s *store.Store, hits []hit, dep bool) (rows, error) {
	var symIDs, modIDs []int64
	for _, h := range hits {
		switch {
		case h.dep != dep:
		case h.module:
			modIDs = append(modIDs, h.id)
		default:
			symIDs = append(symIDs, h.id)
		}
	}
	var r rows
	var err error
	if r.syms, err = s.SymbolsByID(symIDs); err != nil {
		return r, err
	}
	r.mods, err = s.ModulesByID(modIDs)
	return r, err
}

// searchEntry renders a hit of the dependency database; ok is false
// when its row is gone or its package is not in the view.
func (v *depView) searchEntry(r rows, h hit, words []string) (output.Entry, bool) {
	if h.module {
		m, ok := r.mods[h.id]
		p := v.byRepo[m.RepoID]
		if !ok || p == nil {
			return output.Entry{}, false
		}
		e := searchModuleEntry(m, words)
		e.RepoID, e.Tags = 0, []string{p.tag()}
		e.Extra["dep"] = p.id.String()
		return e, true
	}
	sy, ok := r.syms[h.id]
	p := v.byRepo[sy.RepoID]
	if !ok || p == nil {
		return output.Entry{}, false
	}
	e := depEntry(depSym{Symbol: sy, pkg: p})
	e.Doc = snippet(sy.DocText, words)
	return e, true
}

// searchModuleEntry renders a module hit: identity, path (prefixed with
// its repo in a workspace-wide answer), language and kind, snippet.
func searchModuleEntry(m store.Module, words []string) output.Entry {
	return output.Entry{Key: m.Identity, Loc: m.Path, RepoID: m.RepoID, Text: "module " + m.Lang + " " + m.Kind,
		Doc: snippet(m.DocText, words), Extra: map[string]any{"kind": "module", "lang": m.Lang, "path": m.Path}}
}

// snippet returns the sentence of doc sharing the most query words (a
// doc word counts when it starts with a query word), the first sentence
// on a tie, "" for no doc.
func snippet(doc string, words []string) string {
	best, bestScore := "", -1
	for _, s := range sentences(doc) {
		if n := overlap(s, words); n > bestScore {
			best, bestScore = s, n
		}
	}
	return best
}

// overlap counts the query words some word of the sentence starts with.
func overlap(sentence string, words []string) int {
	have := queryWords(sentence)
	n := 0
	for _, w := range words {
		for _, h := range have {
			if strings.HasPrefix(h, w) {
				n++
				break
			}
		}
	}
	return n
}

// sentences splits a doc into one-line sentences: at blank lines and
// after '.', '!' or '?' followed by a space.
func sentences(doc string) []string {
	var out []string
	for _, para := range strings.Split(doc, "\n\n") {
		text := strings.Join(strings.Fields(para), " ")
		start := 0
		for i := 0; i+1 < len(text); i++ {
			if strings.ContainsRune(".!?", rune(text[i])) && text[i+1] == ' ' {
				out = append(out, text[start:i+1])
				start = i + 2
			}
		}
		if start < len(text) {
			out = append(out, text[start:])
		}
	}
	return out
}

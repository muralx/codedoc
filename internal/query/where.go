package query

import (
	"errors"
	"fmt"
	"strings"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/store"
)

// whereLimit is the number of locations where shows by default.
const whereLimit = 5

// strongLead is how many times the fused score of the second location
// the first one must reach to be the answer by itself: the first of
// both the text list and the vector list, followed by hits that are at
// the top of neither.
const strongLead = 1.5

// WhereOpts size a where answer.
type WhereOpts struct {
	Limit int // most locations; 0 means whereLimit
}

// Where answers "where is this done?" (efficiency spec section 5): the
// symbols and sections a search for the words ranks first (see
// rankedHits; modules are left out, they are no location), each with the
// lines that matched and their context (see addFragments). A single
// strong hit, the only one or one whose fused score is strongLead times
// the next, is shown whole instead, as symbol shows it: doc comment and
// body under the budget rules of symbol. The packages the workspace
// depends on are searched with Ctx.Deps only.
func Where(c *Ctx, text string, o WhereOpts) (*output.Response, error) {
	if o.Limit < 0 {
		return nil, errors.New("--limit must not be negative")
	}
	if o.Limit == 0 {
		o.Limit = whereLimit
	}
	repos := c.scope()
	if err := c.EnsureFresh(repos...); err != nil {
		return nil, err
	}
	title := fmt.Sprintf("where %q", text)
	if len(queryWords(text)) == 0 {
		c.notes = append(c.notes, "query has no searchable words")
		return c.respond(repos, c.section(title, nil)), nil
	}
	id, err := c.filterID()
	if err != nil {
		return nil, err
	}
	hits, err := c.rankedHits(text, store.SearchFilter{RepoID: id}, "")
	if err != nil {
		return nil, err
	}
	hits = locations(hits)
	if resp, err := c.whole(repos, title, hits); resp != nil || err != nil {
		return resp, err
	}
	page := hits[:min(len(hits), c.Offset+o.Limit)]
	entries, err := c.searchEntries(page, searchTerms(text))
	if err != nil {
		return nil, err
	}
	sec := c.section(title, entries)
	sec.Total = len(entries) + len(hits) - len(page)
	return c.respond(repos, sec), nil
}

// locations drops the module hits: a module is no place in a file.
func locations(hits []rankedHit) []rankedHit {
	out := hits[:0:0]
	for _, h := range hits {
		if !h.module {
			out = append(out, h)
		}
	}
	return out
}

// whole answers with the first hit shown as symbol shows it when it is a
// workspace symbol that stands alone or leads the next hit by
// strongLead; nil otherwise, and on a page after the first.
func (c *Ctx) whole(repos []*config.Repo, title string, hits []rankedHit) (*output.Response, error) {
	if c.Offset > 0 || len(hits) == 0 || hits[0].dep || (len(hits) > 1 && hits[0].score < strongLead*hits[1].score) {
		return nil, nil
	}
	sy, err := c.Store.SymbolByID(hits[0].id)
	if err != nil {
		return nil, err
	}
	r, err := c.repoOf(sy)
	if err != nil {
		return nil, err
	}
	render := c.detail
	if isDocSymbol(sy) {
		render = c.docDetail
	}
	e, unavailable, err := render(r, sy, SymbolOpts{})
	if err != nil {
		return nil, err
	}
	sec, err := c.fitted(c.Store, sy, e, linesCmd(r.Name+":"+sy.Path))
	if err != nil {
		return nil, err
	}
	// The total counts every location: the line that continues the
	// answer lists the others.
	sec.Title, sec.NoTitle, sec.Total = title, false, len(hits)
	resp := c.respond(repos, sec)
	if unavailable {
		resp.Note = strings.TrimSpace(bodyUnavailable + ". " + resp.Note)
	}
	return resp, nil
}

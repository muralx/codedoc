package query

import (
	"fmt"
	"path"
	"time"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/docs"
	"github.com/muralx/codedoc/internal/docs/write"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/store"
)

// GapsOpts filter a gaps answer.
type GapsOpts struct {
	// Kind is missing, stale or module; "" keeps every gap.
	Kind string
	// Limit keeps the first Limit ranked gaps; 0 keeps all.
	Limit int
}

// now returns the time gaps date their recency from (see Ctx.Now).
func (c *Ctx) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Gaps lists the documentation gaps of the workspace, the --repo repo,
// or one module (scope is a module address), ranked per spec 11.1: each
// entry carries its rank, score and state, the stable key (a module's
// repo:dir/ address), location, signature and the reason for its score.
func Gaps(c *Ctx, scope string, o GapsOpts) (*output.Response, error) {
	switch o.Kind {
	case "", "missing", "stale", "module":
	default:
		return nil, usagef("unknown gap kind %q (want missing, stale or module)", o.Kind)
	}
	repos, keep, resp, err := c.gapScope(scope)
	if err != nil {
		return resp, err
	}
	var all []docs.Gap
	for _, r := range repos {
		id, err := c.repoID(r)
		if err != nil {
			return nil, err
		}
		if id == 0 {
			continue
		}
		gaps, err := docs.RepoGaps(c.Store, docs.RepoRef{ID: id, Name: r.Name, Root: r.Path}, c.now())
		if err != nil {
			return nil, err
		}
		all = append(all, gaps...)
	}
	all = c.writable(filterGaps(docs.Rerank(all), keep, GapsOpts{Kind: o.Kind}), o.Limit)
	entries := make([]output.Entry, len(all))
	for i, g := range all {
		entries[i] = gapEntry(i+1, g)
	}
	return c.respond(repos, c.tagged("gaps", entries)), nil
}

// gapScope returns the repos a gaps answer covers and, for a module
// scope, the filter keeping that module's gaps.
func (c *Ctx) gapScope(scope string) ([]*config.Repo, func(docs.Gap) bool, *output.Response, error) {
	if scope == "" {
		repos := c.scope()
		return repos, nil, nil, c.EnsureFresh(repos...)
	}
	m, resp, err := c.oneModule(scope)
	if err != nil {
		return nil, nil, resp, err
	}
	row, err := c.Store.RepoByID(m.RepoID)
	if err != nil {
		return nil, nil, nil, err
	}
	r, err := c.repoNamed(row.Name)
	if err != nil {
		return nil, nil, nil, err
	}
	return []*config.Repo{r}, inModule(m), nil, c.EnsureFresh(r)
}

// inModule keeps the gaps of a module: the module itself and the
// symbols of the files directly in its directory (or of its file).
func inModule(m store.Module) func(docs.Gap) bool {
	return func(g docs.Gap) bool {
		if g.RepoID != m.RepoID {
			return false
		}
		if g.Kind == "module" {
			return g.Path == m.Path
		}
		return g.Path == m.Path || path.Dir(g.Path) == m.Path
	}
}

func filterGaps(all []docs.Gap, keep func(docs.Gap) bool, o GapsOpts) []docs.Gap {
	var out []docs.Gap
	for _, g := range all {
		switch {
		case keep != nil && !keep(g):
		case o.Kind == "module" && g.Kind != "module":
		case (o.Kind == "missing" || o.Kind == "stale") && string(g.State) != o.Kind:
		default:
			out = append(out, g)
		}
		if o.Limit > 0 && len(out) == o.Limit {
			break
		}
	}
	return out
}

// maxPlanned bounds the gaps checked against the writers when no limit
// is given: the ranked list is long, and only its head is read.
const maxPlanned = 200

// writable keeps, in rank order, the first limit gaps (all when limit is
// 0) whose doc the writers would place: a planned write (never applied)
// must not be refused, as it is for a Python constant, which cannot
// carry a docstring. Only the gaps kept are planned; without a limit,
// those past maxPlanned are kept unchecked.
func (c *Ctx) writable(gaps []docs.Gap, limit int) []docs.Gap {
	var out []docs.Gap
	for i, g := range gaps {
		if limit > 0 && len(out) == limit {
			break
		}
		if (limit > 0 || i < maxPlanned) && !c.plannable(g) {
			continue
		}
		out = append(out, g)
	}
	return out
}

// plannable reports whether the doc writers accept to document a gap.
func (c *Ctx) plannable(g docs.Gap) bool {
	repo, err := c.Store.RepoByID(g.RepoID)
	if err != nil {
		return true
	}
	t, ok := c.gapTarget(repo.Path, g)
	if !ok {
		return true
	}
	_, err = write.Plan(t, "Documents it.")
	return err == nil
}

// gapTarget describes a gap to the doc writers.
func (c *Ctx) gapTarget(root string, g docs.Gap) (write.Target, bool) {
	if g.Kind == "module" {
		mods, err := c.Store.ModulesByIdentity(g.Module)
		if err != nil {
			return write.Target{}, false
		}
		for _, m := range mods {
			if m.RepoID == g.RepoID && m.Path == g.Path {
				return write.Target{Root: root, Path: m.Path, Lang: moduleLangs[m.Lang], ModuleDir: m.Path}, true
			}
		}
		return write.Target{}, false
	}
	sy, err := c.Store.SymbolByKey(g.Key)
	if err != nil {
		return write.Target{}, false
	}
	f, err := c.Store.FileByID(sy.FileID)
	if err != nil {
		return write.Target{}, false
	}
	return symbolTarget(root, sy, f), true
}

// gapEntry renders a ranked gap.
func gapEntry(rank int, g docs.Gap) output.Entry {
	loc, repoID := fmt.Sprintf("%s:%d", g.Path, g.Line), g.RepoID
	if g.Kind == "module" {
		loc, repoID = "", 0 // the key is the module's repo:dir/ address
	}
	return output.Entry{Key: g.Key, Loc: loc, Text: g.Signature, Doc: g.Reason, RepoID: repoID,
		Tags: []string{fmt.Sprintf("#%d", rank), fmt.Sprintf("score %.2f", g.Score), docTag(string(g.State))},
		Extra: map[string]any{"rank": rank, "score": g.Score, "state": string(g.State), "kind": g.Kind,
			"module": g.Module, "reason": g.Reason}}
}

package query

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/store"
)

// graphTargetExact is graphTarget for a query that tries something else
// for an address no symbol has (see exactFirst).
func (c *Ctx) graphTargetExact(addr string) (store.Symbol, *output.Response, error) {
	defer c.exactFirst()()
	return c.graphTarget(addr)
}

// oneModuleExact is oneModule without the nearest modules (see
// exactFirst).
func (c *Ctx) oneModuleExact(addr string) (store.Module, *output.Response, error) {
	defer c.exactFirst()()
	return c.oneModule(addr)
}

// bestModule answers an ambiguous module address: the module of the
// current repo, else the one with the shortest path, is returned and the
// note names the others.
func (c *Ctx) bestModule(mods []store.Module) store.Module {
	current := int64(0)
	if c.Repo != nil {
		current, _ = c.repoID(c.Repo)
	}
	slices.SortStableFunc(mods, func(a, b store.Module) int {
		return cmp.Or(cmp.Compare(boolRank(a.RepoID != current), boolRank(b.RepoID != current)),
			cmp.Compare(len(a.Path), len(b.Path)), cmp.Compare(a.Path, b.Path))
	})
	others := make([]string, len(mods)-1)
	for i, m := range mods[1:] {
		others[i] = fmt.Sprintf("%s (%s:%s)", m.Identity, c.repoNames()[m.RepoID], m.Path)
	}
	c.notes = append(c.notes, "also: "+shownNames(others, alsoShown))
	return mods[0]
}

// unmatchedModule answers a module address that names no module of
// repos, as unmatched does for a symbol: the modules whose identity or
// directory is nearest to it (see nearness), the only plausible one
// returned and noted, several listed with ErrNearest.
func (c *Ctx) unmatchedModule(addr string, repos []*config.Repo) (store.Module, *output.Response, error) {
	missing := fmt.Errorf("module %s: %w", addr, ErrNotFound)
	if c.noNearest() {
		return store.Module{}, c.respond(repos), missing
	}
	mods, one, err := c.nearModules(addr, repos)
	switch {
	case err != nil:
		return store.Module{}, nil, err
	case len(mods) == 0:
		return store.Module{}, nil, missing
	case one:
		c.notes = append(c.notes, "answered for "+mods[0].Identity)
		return mods[0], nil, nil
	}
	entries := make([]output.Entry, min(len(mods), nearestCap))
	for i := range entries {
		if entries[i], err = c.moduleEntry(mods[i]); err != nil {
			return store.Module{}, nil, err
		}
	}
	return store.Module{}, c.respond(repos, c.section(nearestTitle, entries)), fmt.Errorf("module %s: %w", addr, ErrNearest)
}

// nearModules returns the modules of repos nearest to an address,
// nearest first: by the nearer of their identity and their directory to
// the address without its repo prefix and trailing slash. one tells that
// the first is the only module that equals it ignoring case or ends
// with it.
func (c *Ctx) nearModules(addr string, repos []*config.Repo) (mods []store.Module, one bool, err error) {
	if _, dir, ok := strings.Cut(addr, ":"); ok {
		addr = dir
	}
	want := strings.ToLower(strings.Trim(addr, "/"))
	tier := map[int64]int{}
	count := make([]int, nearNot+1)
	for _, r := range repos {
		id, err := c.repoID(r)
		if err != nil {
			return nil, false, err
		}
		all, err := c.Store.ModulesByRepo(id)
		if err != nil {
			return nil, false, err
		}
		for _, m := range all {
			byName, _ := nearness(strings.ToLower(m.Identity), want)
			byPath, _ := nearness(strings.ToLower(m.Path), want)
			if t := min(byName, byPath); t != nearNot {
				tier[m.ID] = t
				count[t]++
				mods = append(mods, m)
			}
		}
	}
	slices.SortStableFunc(mods, func(a, b store.Module) int { return cmp.Compare(tier[a.ID], tier[b.ID]) })
	return mods, count[nearEqual] == 1 || count[nearEqual] == 0 && count[nearSuffix] == 1, nil
}

package query

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/store"
)

// Deps lists the modules a module imports, walked to o.Depth as a tree.
// Without CrossRepo the walk stays in the module's repo and the note
// counts the cross-repo imports it left out.
func Deps(c *Ctx, addr string, o GraphOpts) (*output.Response, error) {
	return c.moduleQuery(addr, o, false)
}

// Dependents lists the modules importing a module, like Deps reversed.
func Dependents(c *Ctx, addr string, o GraphOpts) (*output.Response, error) {
	return c.moduleQuery(addr, o, true)
}

func (c *Ctx) moduleQuery(addr string, o GraphOpts, inbound bool) (*output.Response, error) {
	if o.Depth < 1 {
		return nil, errors.New("--depth must be at least 1")
	}
	if err := c.EnsureFresh(c.Workspace.Repos...); err != nil {
		return nil, err
	}
	mod, resp, err := c.oneModule(addr)
	if err != nil {
		return resp, err
	}
	c.draws = append(c.draws, mod.RepoID)
	wo := store.ModuleWalkOpts{Inbound: inbound, Depth: o.Depth, Limit: walkRows}
	if !o.CrossRepo {
		wo.RepoID = mod.RepoID
	}
	steps, err := c.Store.ModuleWalk(mod.ID, wo)
	if err != nil {
		return nil, err
	}
	steps, _ = firstVisits(steps)
	entries, err := c.moduleTree(mod, steps)
	if err != nil {
		return nil, err
	}
	if !o.CrossRepo {
		if err := c.noteHidden(mod, steps, inbound); err != nil {
			return nil, err
		}
	}
	title := "dependencies of "
	if inbound {
		title = "dependents of "
	}
	return c.respond(c.Workspace.Repos, c.tagged(title+mod.Identity, entries)), nil
}

// moduleTree renders the walk depth first, children by identity.
func (c *Ctx) moduleTree(root store.Module, steps []store.Step) ([]output.Entry, error) {
	mods, err := c.Store.ModulesByID(nodes(steps))
	if err != nil {
		return nil, err
	}
	var out []output.Entry
	err = dfs(steps, root.ID, func(a, b store.Step) bool { return mods[a.Node].Identity < mods[b.Node].Identity },
		func(st store.Step) (bool, error) {
			e, err := c.moduleEntry(mods[st.Node])
			e.Depth = st.Depth - 1
			if mods[st.Node].RepoID != root.RepoID {
				e.Tags = append(e.Tags, "cross-repo")
			}
			out = append(out, e)
			return true, err
		})
	return out, err
}

// noteHidden notes how many cross-repo module edges a same-repo walk
// left out from the modules it reached.
func (c *Ctx) noteHidden(root store.Module, steps []store.Step, inbound bool) error {
	var others []int64
	for _, id := range append([]int64{root.ID}, nodes(steps)...) {
		edges, err := c.Store.ModuleEdgesFrom(id)
		if inbound {
			edges, err = c.Store.ModuleEdgesTo(id)
		}
		if err != nil {
			return err
		}
		for _, e := range edges {
			if inbound {
				others = append(others, e.Src)
			} else {
				others = append(others, e.Dst)
			}
		}
	}
	mods, err := c.Store.ModulesByID(others)
	if err != nil {
		return err
	}
	hidden := 0
	for _, m := range mods {
		if m.RepoID != root.RepoID {
			hidden++
		}
	}
	if hidden > 0 {
		c.notes = append(c.notes, fmt.Sprintf("%d cross-repo %s hidden; rerun with --cross-repo", hidden,
			plural(hidden, "module", "modules")))
	}
	return nil
}

func nodes(steps []store.Step) []int64 {
	out := make([]int64, len(steps))
	for i, st := range steps {
		out[i] = st.Node
	}
	return out
}

// oneModule resolves the address of a required module argument; see
// modules. No match is answered for the nearest module (see
// unmatchedModule) and several for the best one (see bestModule); when
// resolution is strict they return ErrNotFound and ErrAmbiguous, each
// with a response to print first.
func (c *Ctx) oneModule(addr string) (store.Module, *output.Response, error) {
	mods, err := c.modules(addr)
	if err != nil {
		return store.Module{}, nil, err
	}
	if len(mods) == 1 {
		return mods[0], nil, nil
	}
	repos := c.scope()
	if repo, _, ok := strings.Cut(addr, ":"); ok {
		if r, err := c.repoNamed(repo); err == nil {
			repos = []*config.Repo{r}
		}
	}
	if err := c.EnsureFresh(repos...); err != nil {
		return store.Module{}, nil, err
	}
	if len(mods) == 0 {
		return c.unmatchedModule(addr, repos)
	}
	if !c.noBest() {
		return c.bestModule(mods), nil, nil
	}
	entries := make([]output.Entry, len(mods))
	for i, m := range mods {
		if entries[i], err = c.moduleEntry(m); err != nil {
			return store.Module{}, nil, err
		}
	}
	return store.Module{}, c.respond(repos, c.section("candidates", entries)),
		fmt.Errorf("module %s: %w (%d candidates)", addr, ErrAmbiguous, len(mods))
}

// modules finds the modules an address names: repo:dir, else a module
// identity (github.com/acme/x/store, @acme/domain), else a directory of
// the current repo resolved like an outline path.
func (c *Ctx) modules(addr string) ([]store.Module, error) {
	if repo, dir, ok := strings.Cut(addr, ":"); ok {
		r, err := c.repoNamed(repo)
		if err != nil {
			return nil, nil
		}
		return c.modulesAt(r.Name, cleanDir(dir))
	}
	mods, err := c.Store.ModulesByIdentity(strings.TrimSuffix(addr, "/"))
	if err != nil || len(mods) > 0 {
		return mods, err
	}
	r, dir, err := c.splitTarget(addr)
	if err != nil {
		return nil, nil
	}
	return c.modulesAt(r.Name, dir)
}

// modulesAt returns the modules of a repo at a directory.
func (c *Ctx) modulesAt(repo, dir string) ([]store.Module, error) {
	r, err := c.repoNamed(repo)
	if err != nil {
		return nil, err
	}
	id, err := c.repoID(r)
	if err != nil || id == 0 {
		return nil, err
	}
	all, err := c.Store.ModulesByRepo(id)
	var out []store.Module
	for _, m := range all {
		if m.Path == dir {
			out = append(out, m)
		}
	}
	return out, err
}

func cleanDir(dir string) string { return path.Clean("./" + strings.TrimSuffix(dir, "/")) }

// moduleEntry renders a module: identity, repo:dir, language and kind.
func (c *Ctx) moduleEntry(m store.Module) (output.Entry, error) {
	row, err := c.Store.RepoByID(m.RepoID)
	return output.Entry{Key: m.Identity, Loc: row.Name + ":" + m.Path, Text: m.Lang + " " + m.Kind,
		Extra: map[string]any{"repo": row.Name, "path": m.Path, "lang": m.Lang, "kind": m.Kind}}, err
}

// dfs visits the steps depth first from root, siblings ordered by less;
// visit returns whether to descend below a step.
func dfs(steps []store.Step, root int64, less func(a, b store.Step) bool,
	visit func(store.Step) (bool, error)) error {
	children := map[int64][]store.Step{}
	for _, st := range steps {
		children[st.Parent] = append(children[st.Parent], st)
	}
	for _, kids := range children {
		sort.SliceStable(kids, func(i, j int) bool { return less(kids[i], kids[j]) })
	}
	var walk func(parent int64) error
	walk = func(parent int64) error {
		for _, st := range children[parent] {
			deeper, err := visit(st)
			if err != nil {
				return err
			}
			if deeper && st.Node != 0 {
				if err := walk(st.Node); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(root)
}

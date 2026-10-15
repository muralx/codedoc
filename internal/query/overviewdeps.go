package query

import (
	"fmt"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/store"
)

// overviewDeps lists the third-party packages a repo depends on (deps
// spec section 7): one entry per package version with its state and,
// when indexed, its symbol count. Workspace packages (skipped uses) are
// not dependencies and are left out, and so are the packages the code
// does not import (unused uses). Dependencies appear here only:
// they count in no total, coverage or gap of the overview.
func (c *Ctx) overviewDeps(r *config.Repo, id int64) ([]output.Entry, error) {
	uses, err := c.Store.DepUses(id)
	if err != nil {
		return nil, err
	}
	var out []output.Entry
	seen := map[string]bool{}
	for _, u := range uses {
		key := u.PackageID()
		if key == "" {
			key = u.Eco + "/" + u.Name
		}
		if u.State == store.DepSkipped || u.State == store.DepUnused || seen[key] {
			continue
		}
		seen[key] = true
		e := output.Entry{Key: key, Text: r.Name + " " + u.State,
			Extra: map[string]any{"repo": r.Name, "state": u.State, "manifest": u.Manifest}}
		if u.State == store.DepIndexed {
			n, err := c.depSymbolCount(key)
			if err != nil {
				return nil, err
			}
			e.Text += fmt.Sprintf(" symbols:%d", n)
			e.Extra["symbols"] = n
		}
		out = append(out, e)
	}
	return out, nil
}

// depSymbolCount counts the symbols deps.db holds of a package version,
// 0 when the query does not see it.
func (c *Ctx) depSymbolCount(id string) (int, error) {
	for _, p := range c.deps().named(id) {
		_, n, err := c.view.s.RepoStats(p.repoID)
		return n, err
	}
	return 0, nil
}

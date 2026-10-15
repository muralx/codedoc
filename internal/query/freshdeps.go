package query

import (
	"context"
	"time"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/deps"
)

// depsBudget is how long one query spends indexing the package versions
// deps.db lacks (deps spec section 9); what is left stays pending.
const depsBudget = 2 * time.Second

// ensureDeps runs the dependency part of the freshness check for the
// repos a query touches, once per repo and context: discovery again
// where a manifest, a lock file or an install marker changed, then at
// most depsBudget of indexing (see deps.Sync). It leaves in DepsPending
// the package versions of the repos checked so far that still wait. A
// failure is noted on the answer, never returned: the workspace index
// answers without its dependencies.
func (c *Ctx) ensureDeps(repos []*config.Repo) {
	if c.DepsDB == "" {
		return
	}
	var todo []*config.Repo
	for _, r := range repos {
		if !c.depsChecked[r.Name] {
			todo = append(todo, r)
		}
	}
	if len(todo) == 0 {
		return
	}
	if c.depsChecked == nil {
		c.depsChecked = map[string]bool{}
	}
	for _, r := range todo {
		c.depsChecked[r.Name] = true
		c.depsRepos = append(c.depsRepos, r)
	}
	opts := deps.Options{DB: c.DepsDB, Deadline: time.Now().Add(depsBudget)}
	if _, err := deps.Sync(context.Background(), c.Store, c.Workspace, todo, opts); err != nil {
		c.notes = append(c.notes, "deps: "+err.Error())
	}
	if n, err := deps.Pending(c.Store, c.depsRepos); err == nil {
		c.DepsPending = n
	}
}

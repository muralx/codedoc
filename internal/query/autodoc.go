package query

import (
	"errors"
	"strings"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/docs"
	"github.com/muralx/codedoc/internal/store"
)

// RepoGaps returns the documentation gaps of one repo in rank order
// (spec 11.1), as the gaps query lists them: after the freshness check,
// keeping only gaps the doc writers can place, the first limit of them
// (all when limit is 0). It is what an unattended documentation run
// works from.
func RepoGaps(c *Ctx, r *config.Repo, limit int) ([]docs.Gap, error) {
	if err := c.EnsureFresh(r); err != nil {
		return nil, err
	}
	id, err := c.repoID(r)
	if err != nil || id == 0 {
		return nil, err
	}
	gaps, err := docs.RepoGaps(c.Store, docs.RepoRef{ID: id, Name: r.Name, Root: r.Path}, c.now())
	if err != nil {
		return nil, err
	}
	return c.writable(gaps, limit), nil
}

// DocStates returns the stored doc state (ok, missing, stale, ...) of
// each key: a symbol's stable key, or a module's repo:dir/ key, whose
// state is ok only when every module at that directory is ok. Keys that
// name nothing are absent.
func DocStates(c *Ctx, keys []string) (map[string]string, error) {
	out := map[string]string{}
	for _, k := range keys {
		if strings.HasSuffix(k, "/") {
			mods, err := c.modules(k)
			if err != nil {
				return nil, err
			}
			for _, m := range mods {
				if st, seen := out[k]; !seen || st == string(docs.OK) {
					out[k] = m.DocState
				}
			}
			continue
		}
		sy, err := c.Store.SymbolByKey(k)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out[k] = sy.DocState
	}
	return out, nil
}

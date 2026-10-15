package query

import (
	"errors"
	"path"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/gitmeta"
)

// ChangedKeys returns the keys of what changed in repo r between ref (a
// commit or tree) and the working tree, the way Changes tells it, in the
// files keep accepts (every file when keep is nil): the stable key of
// each symbol that was added or moved or whose body or doc differs, and
// the repo:dir/ key of the directory of each such file, which is the key
// of the modules there. Documents and their sections are left out.
// Documentation upkeep takes its candidates from it.
func ChangedKeys(c *Ctx, r *config.Repo, ref string, keep func(path string) bool) (keys map[string]bool, err error) {
	if err := c.EnsureFresh(r); err != nil {
		return nil, err
	}
	changed, err := gitmeta.ChangesSince(r.Path, ref)
	if err != nil || len(changed) == 0 {
		return nil, err
	}
	id, err := c.repoID(r)
	if err != nil {
		return nil, err
	}
	blobs, err := gitmeta.OpenBlobs(r.Path, ref)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, blobs.Close()) }()
	fcs := make([]*fileChange, len(changed))
	for i, ch := range changed {
		if fcs[i], err = c.fileChanges(r, id, blobs, ch); err != nil {
			return nil, err
		}
	}
	matchMoves(fcs)
	keys = map[string]bool{}
	for _, fc := range fcs {
		if keep == nil || keep(fc.ch.Path) {
			fc.addKeys(r.Name, keys)
		}
	}
	return keys, nil
}

// addKeys adds the keys of the file's changed symbols and, when it has
// any symbol, of its directory.
func (fc *fileChange) addKeys(repo string, keys map[string]bool) {
	for i, sy := range fc.cur {
		if isDocSymbol(sy) {
			continue
		}
		keys[repo+":"+path.Dir(fc.ch.Path)+"/"] = true
		if p, had := fc.pair[i]; len(fc.tags(sy, p, had)) > 0 {
			keys[sy.StableKey] = true
		}
	}
}

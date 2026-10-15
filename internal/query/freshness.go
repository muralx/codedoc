package query

import (
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/gitmeta"
	"github.com/muralx/codedoc/internal/index"
)

// defaultFreshMax is how many added plus changed files a query re-extracts
// before answering; CODEDOC_FRESH_MAX_FILES overrides it.
const defaultFreshMax = 200

// EnsureFresh runs the freshness check for the repos a query will touch,
// once per repo and context, then that of their dependencies (see
// ensureDeps).
func (c *Ctx) EnsureFresh(repos ...*config.Repo) error {
	maxFiles, err := FreshMax()
	if err != nil {
		return err
	}
	if c.Fresh == nil {
		c.Fresh = map[string]index.Freshness{}
	}
	for _, r := range repos {
		if _, done := c.Fresh[r.Name]; done {
			continue
		}
		f, err := c.freshness(r, maxFiles)
		if err != nil {
			return fmt.Errorf("freshness check of %s: %w", r.Name, err)
		}
		c.Fresh[r.Name] = f
	}
	c.ensureDeps(repos)
	return nil
}

// EnsureSynced brings repos fully up to date, as codedoc index does for
// them: every added, changed and deleted file is re-extracted whatever
// CODEDOC_FRESH_MAX_FILES says, and a check this context already ran is
// redone. A repo whose directory is gone is reported Missing.
func (c *Ctx) EnsureSynced(repos ...*config.Repo) error {
	if c.Fresh == nil {
		c.Fresh = map[string]index.Freshness{}
	}
	for _, r := range repos {
		f, err := index.Ensure(c.Store, r, c.indexOptions(math.MaxInt))
		if err != nil {
			return fmt.Errorf("index %s: %w", r.Name, err)
		}
		c.Fresh[r.Name] = f
	}
	return nil
}

// FreshMax returns how many added plus changed files a query
// re-extracts before answering: CODEDOC_FRESH_MAX_FILES, else 200. Over
// it a query answers from the index as it is, and only codedoc index
// brings the index up to date.
func FreshMax() (int, error) {
	v := os.Getenv("CODEDOC_FRESH_MAX_FILES")
	if v == "" {
		return defaultFreshMax, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("CODEDOC_FRESH_MAX_FILES=%q is not a file count", v)
	}
	return n, nil
}

// freshness checks one repo before a query: a repo whose directory is
// gone is answered from its index, reported Missing; when the daemon
// reports the repo fresh, the index is trusted and only the live head
// and dirty count are read; otherwise index.Ensure runs its stat pass.
func (c *Ctx) freshness(r *config.Repo, maxFiles int) (index.Freshness, error) {
	if info, err := os.Stat(r.Path); err != nil || !info.IsDir() {
		return index.Freshness{Missing: true}, nil
	}
	if c.DaemonFresh == nil || !c.DaemonFresh(c.Workspace.Name, r) {
		return index.Ensure(c.Store, r, c.indexOptions(maxFiles))
	}
	f := index.Freshness{Fresh: true}
	head, err := gitmeta.Head(r.Path)
	if errors.Is(err, gitmeta.ErrNotRepo) {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	dirty, err := gitmeta.DirtyFiles(r.Path)
	f.Branch, f.Commit, f.Dirty = head.Branch, head.Commit, len(dirty)
	return f, err
}

package query

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/store"
)

// lineFile is the file codedoc lines prints: its content on disk and
// the symbols an index places in it.
type lineFile struct {
	// repos are the repos the answer was computed from.
	repos []*config.Repo
	// title names the file in the section title, loc in the entry, and
	// addr is the address that names it from any working directory.
	title, loc, addr string
	// tags are the tags of its entries: the package of a dependency file.
	tags []string
	src  []byte
	syms []store.Symbol
}

// lineFile resolves the path argument of lines: a file of a workspace
// repo, read from disk and compared with its row (see indexedSymbols),
// or, when the workspace index has no row for it, a file of a
// dependency (see depLineFile). A file on disk neither index knows is
// returned without symbols and the note says why it is not indexed (see
// whyNot). A file that does not exist is answered for the one it may
// mean (see lineFileLike).
func (c *Ctx) lineFile(target string) (lineFile, *output.Response, error) {
	r, p, terr := c.splitTarget(target)
	repos := c.scope()
	if terr == nil {
		repos = []*config.Repo{r}
	}
	if err := c.EnsureFresh(repos...); err != nil {
		return lineFile{}, nil, err
	}
	if terr != nil {
		f, ok, err := c.depLineFile(target, repos)
		switch {
		case err != nil || ok:
			return f, nil, err
		case errors.Is(terr, ErrOutside):
			return lineFile{}, nil, terr
		}
		return lineFile{}, nil, fmt.Errorf("lines %s: not inside a repo; write repo:path", target)
	}
	src, err := os.ReadFile(filepath.Join(r.Path, filepath.FromSlash(p)))
	if err != nil {
		return c.lineFileLike(target, r, p)
	}
	syms, found, err := c.indexedSymbols(r, p, src)
	if err != nil || found {
		return lineFile{repos: repos, title: p, loc: p, addr: r.Name + ":" + p, src: src, syms: syms}, nil, err
	}
	if f, ok, err := c.depLineFile(target, repos); err != nil || ok {
		return f, nil, err
	}
	why, err := c.whyNot(r, p)
	c.notes = append(c.notes, fmt.Sprintf("%s is not indexed: %s", p, why))
	return lineFile{repos: repos, title: p, loc: p, addr: r.Name + ":" + p, src: src}, nil, err
}

// lineFileLike resolves a path of lines that names no file of r to the
// only indexed file it may mean, noted; several are listed with
// ErrNearest and none is ErrNotFound (see missingPath).
func (c *Ctx) lineFileLike(target string, r *config.Repo, p string) (lineFile, *output.Response, error) {
	var f lineFile
	resp, err := c.missingPath("lines", target, r, p, false, func(addr string) (*output.Response, error) {
		var resp *output.Response
		var err error
		f, resp, err = c.lineFile(addr)
		return resp, err
	})
	return f, resp, err
}

// depLineFile resolves the path argument of lines to an indexed file of
// a dependency (see depFileAt), read from the directory its package is
// installed in; ok is false when the path names none. A file that no
// longer is the one that was indexed is noted: its symbols may be off.
func (c *Ctx) depLineFile(target string, repos []*config.Repo) (lineFile, bool, error) {
	f, ok, err := c.depFileAt(target)
	if err != nil || !ok {
		return lineFile{}, false, err
	}
	src, err := os.ReadFile(f.abs)
	if err != nil {
		return lineFile{}, false, nil
	}
	if index.ContentHash(src) != f.file.Hash {
		c.notes = append(c.notes, fmt.Sprintf("%s differs from the %s that was indexed", f.abs, f.pkg.id))
	}
	found, err := c.depFileSymbols(f)
	if err != nil {
		return lineFile{}, false, err
	}
	syms := make([]store.Symbol, len(found))
	for i, d := range found {
		syms[i] = d.Symbol
	}
	return lineFile{repos: repos, title: f.file.Path + " [" + f.pkg.tag() + "]", loc: f.abs, addr: f.abs, src: src,
		syms: syms, tags: []string{f.pkg.tag()}}, true, nil
}

// indexedSymbols returns the symbols the index holds for a file whose
// content on disk is src, in line order. A file whose row no longer
// matches src (the freshness check left it, see FreshMax, or it changed
// since) is re-indexed first and noted; found is false for a file
// without a row.
func (c *Ctx) indexedSymbols(r *config.Repo, p string, src []byte) (syms []store.Symbol, found bool, err error) {
	id, err := c.repoID(r)
	if err != nil {
		return nil, false, err
	}
	f, err := c.Store.FileByPath(id, p)
	if err == nil && f.Hash != index.ContentHash(src) {
		c.notes = append(c.notes, fmt.Sprintf("%s changed since it was indexed: re-indexed before answering", p))
		if err := c.reindex(r, p); err != nil {
			return nil, false, err
		}
		f, err = c.Store.FileByPath(id, p)
	}
	if errors.Is(err, store.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	syms, err = c.Store.SymbolsInFile(f.ID)
	return syms, true, err
}

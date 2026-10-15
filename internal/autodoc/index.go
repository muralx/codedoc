package autodoc

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/parse"
	"github.com/muralx/codedoc/internal/query"
	"github.com/muralx/codedoc/internal/store"
)

// Indexed serves Gaps and Indexer from a workspace index: the ranked
// writable gaps of the gaps query, synchronous re-indexing of the files
// a run wrote, and the stored doc state of stable keys.
type Indexed struct {
	// Ctx carries the store and the workspace; its Embedder, when set,
	// embeds the vectors of re-indexed files.
	Ctx *query.Ctx
}

// Next returns the first limit ranked gaps of repo (all when limit <= 0).
func (x Indexed) Next(repo string, limit int) ([]Gap, error) {
	r, err := x.repo(repo)
	if err != nil {
		return nil, err
	}
	gaps, err := query.RepoGaps(x.Ctx, r, max(limit, 0))
	if err != nil {
		return nil, err
	}
	out := make([]Gap, len(gaps))
	for i, g := range gaps {
		out[i] = Gap{Key: g.Key, Module: g.Module, Path: g.Path, Line: g.Line, State: string(g.State), Score: g.Score}
	}
	return out, nil
}

// Reindex re-extracts the repo-relative paths of repo under the write
// lock and resolves what that leaves pending.
func (x Indexed) Reindex(repo string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	r, err := x.repo(repo)
	if err != nil {
		return err
	}
	ws := x.Ctx.Workspace
	return index.Files(x.Ctx.Store, r, index.Options{Workspace: ws.Name, Exclude: ws.Exclude,
		RepoOrder: ws.RepoNames(), Embedder: x.Ctx.Embedder}, paths)
}

// States returns the stored doc state of each key it knows.
func (x Indexed) States(keys []string) (map[string]string, error) {
	return query.DocStates(x.Ctx, keys)
}

// Docs returns the doc hash and file of every symbol and module of
// repo, as stored. Modules sharing a directory share its repo:dir/ key
// and one combined hash.
func (x Indexed) Docs(repo string) (map[string]DocAt, error) {
	r, err := x.repo(repo)
	if err != nil {
		return nil, err
	}
	out := map[string]DocAt{}
	row, err := x.Ctx.Store.RepoByPath(r.Path)
	if errors.Is(err, store.ErrNotFound) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	if err := x.symbolDocs(row.ID, out); err != nil {
		return nil, err
	}
	mods, err := x.Ctx.Store.ModulesByRepo(row.ID)
	for _, m := range mods {
		k := repo + ":" + m.Path + "/"
		prev := out[k]
		out[k] = DocAt{Path: m.DocSource, Hash: docHash(prev.Hash + m.Lang + "\x00" + m.DocText)}
	}
	return out, err
}

// symbolDocs adds the symbols of a repo, one language at a time.
func (x Indexed) symbolDocs(repoID int64, out map[string]DocAt) error {
	files, err := x.Ctx.Store.FilesByRepo(repoID)
	if err != nil {
		return err
	}
	langs := map[string]bool{}
	for _, f := range files {
		if f.Lang == "" || langs[f.Lang] || parse.IsDoc(f.Lang) {
			continue // documents have no docs for autodoc to write
		}
		langs[f.Lang] = true
		syms, err := x.Ctx.Store.RepoSymbols(repoID, f.Lang)
		if err != nil {
			return err
		}
		for _, s := range syms {
			out[s.StableKey] = DocAt{Path: s.Path, Hash: docHash(s.DocText)}
		}
	}
	return nil
}

// docHash is "" for no doc, else a SHA-256 of the text.
func docHash(text string) string {
	if text == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func (x Indexed) repo(name string) (*config.Repo, error) {
	for _, r := range x.Ctx.Workspace.Repos {
		if r.Name == name {
			return r, nil
		}
	}
	return nil, fmt.Errorf("autodoc: workspace %s has no repo named %q", x.Ctx.Workspace.Name, name)
}

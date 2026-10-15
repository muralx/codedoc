package query

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/docs/write"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/parse"
	"github.com/muralx/codedoc/internal/stablekey"
	"github.com/muralx/codedoc/internal/store"
)

// DocSet writes text as the doc of the symbol or module addr names
// (spec 10), re-indexes the written file synchronously and answers with
// the new doc span and state. A module is addressed by identity or by
// repo:dir/ (trailing slash); anything else is a symbol address. An
// explicit doc set marks a symbol's doc ok even when the text did not
// change: the caller reviewed it against the current body. A doc that
// could not be written is a *DocSetError: what failed and what to do.
func DocSet(c *Ctx, addr, text string) (*output.Response, error) {
	w, resp, err := c.writeDoc(addr, text)
	if err != nil {
		return resp, explainDocSet(addr, resp, err)
	}
	if err := c.reindex(w.repo, w.res.Path); err != nil {
		return nil, err
	}
	e, err := c.docWritten(w)
	if err != nil {
		return nil, err
	}
	return c.respond([]*config.Repo{w.repo}, output.Section{Title: "doc set", Entries: []output.Entry{e}}), nil
}

// docWrite is a doc written into a source file the index has not read
// yet: the repo, what write.Apply did and whose doc it is (the stable
// key of a symbol, else the module).
type docWrite struct {
	repo *config.Repo
	res  write.Result
	key  string
	mod  store.Module
}

// writeDoc resolves a doc set address and writes text as its doc. An
// address that names nothing or several things comes back with the
// response one and oneModule built for it.
func (c *Ctx) writeDoc(addr, text string) (docWrite, *output.Response, error) {
	defer c.strictly()() // a doc is never written to a nearest or a best candidate
	if c.isModuleAddr(addr) {
		m, resp, err := c.oneModule(addr)
		if err != nil {
			return docWrite{}, resp, err
		}
		w, err := c.writeModuleDoc(m, text)
		return w, nil, err
	}
	sy, resp, err := c.one(addr)
	if err != nil {
		return docWrite{}, resp, err
	}
	if isDocSymbol(sy) {
		return docWrite{}, nil, &DocSetError{Addr: addr, Do: "edit the file",
			What: fmt.Sprintf("is a %s of %s, which has no doc to set", sy.Kind, sy.Path)}
	}
	r, err := c.repoOf(sy)
	if err != nil {
		return docWrite{}, nil, err
	}
	f, err := c.Store.FileByID(sy.FileID)
	if err != nil {
		return docWrite{}, nil, err
	}
	res, err := write.Apply(symbolTarget(r.Path, sy, f), text)
	return docWrite{repo: r, res: res, key: sy.StableKey}, nil, err
}

// docWritten describes a written doc once its file is re-indexed: a
// symbol's doc is marked reviewed and answered with its span and state,
// a module's with its doc source (see moduleDocEntry).
func (c *Ctx) docWritten(w docWrite) (output.Entry, error) {
	if w.key == "" {
		return c.moduleDocEntry(w)
	}
	after, err := c.reviewed(w.key)
	if err != nil {
		return output.Entry{}, err
	}
	if err := c.noteSharedSpec(after); err != nil {
		return output.Entry{}, err
	}
	return output.Entry{Key: after.StableKey, Loc: spanLoc(after.Path, after.DocStartLine, after.DocEndLine),
		Text: "doc " + after.DocState, Doc: firstSentence(after.DocText), RepoID: after.RepoID,
		Extra: map[string]any{"state": after.DocState, "doc": after.DocText}}, nil
}

// noteSharedSpec notes the other names a doc written above a multi-name
// declaration (var A, B = 1, 2; a field list x, y int) documents too:
// the symbols of the same kind declared on the same line.
func (c *Ctx) noteSharedSpec(sy store.Symbol) error {
	syms, err := c.Store.SymbolsInFile(sy.FileID)
	if err != nil {
		return err
	}
	var others []string
	for _, o := range syms {
		if o.ID != sy.ID && o.Kind == sy.Kind && o.StartLine == sy.StartLine &&
			o.ContainerID == sy.ContainerID {
			others = append(others, o.Name)
		}
	}
	if len(others) > 0 {
		c.notes = append(c.notes, fmt.Sprintf("the doc of %s also documents %s (declared together)", sy.Name,
			strings.Join(others, ", ")))
	}
	return nil
}

// isModuleAddr reports whether a doc set address names a module: a
// directory path (trailing slash), repo:dir with a repo of the workspace
// (resolved like outline, explain and deps do), or a known module
// identity.
func (c *Ctx) isModuleAddr(addr string) bool {
	if strings.HasSuffix(addr, "/") {
		return true
	}
	if repo, _, ok := strings.Cut(addr, ":"); ok && ParseAddr(addr).Kind == AddrBare {
		if _, err := c.repoNamed(repo); err == nil {
			return true
		}
	}
	mods, err := c.Store.ModulesByIdentity(addr)
	return err == nil && len(mods) > 0
}

// symbolTarget describes a symbol to the doc writers. TypeScript
// qualified names start with the path the extractor saw; Java methods
// and constructors carry their signature hash.
func symbolTarget(root string, sy store.Symbol, f store.File) write.Target {
	t := write.Target{Root: root, Path: sy.Path, Lang: sy.Lang, Qualified: sy.Qualified, Generated: f.Generated}
	if parse.Family(sy.Lang) == "ts" {
		if i := strings.LastIndex(sy.Qualified, "/"); i > 0 {
			t.ExtractPath = sy.Qualified[:i]
		}
	}
	if k, err := stablekey.Parse(sy.StableKey); err == nil && sy.Lang == parse.Java &&
		(sy.Kind == "method" || sy.Kind == "constructor") {
		t.SigHash, _, _ = strings.Cut(k.SigHash, "#")
	}
	return t
}

// moduleLangs maps a module's language family to the precise language
// the doc writers place its doc for.
var moduleLangs = map[string]string{"go": parse.Go, "ts": parse.TypeScript, "py": parse.Python, "java": parse.Java}

// writeModuleDoc writes the doc of a module into its doc file.
func (c *Ctx) writeModuleDoc(m store.Module, text string) (docWrite, error) {
	row, err := c.Store.RepoByID(m.RepoID)
	if err != nil {
		return docWrite{}, err
	}
	r, err := c.repoNamed(row.Name)
	if err != nil {
		return docWrite{}, err
	}
	res, err := write.Apply(write.Target{Root: r.Path, Path: m.Path, Lang: moduleLangs[m.Lang], ModuleDir: m.Path}, text)
	return docWrite{repo: r, res: res, mod: m}, err
}

// moduleDocEntry answers a written module doc with the module's doc
// source and state, noting the other files that carry a package comment.
func (c *Ctx) moduleDocEntry(w docWrite) (output.Entry, error) {
	for _, o := range w.res.Others {
		c.notes = append(c.notes, fmt.Sprintf("%s also carries a package comment; keep one (the %s one is the package doc)",
			o, path.Base(w.res.Path)))
	}
	after, err := c.Store.ModuleByID(w.mod.ID)
	if err != nil {
		return output.Entry{}, err
	}
	return output.Entry{Key: after.Identity, Loc: spanLoc(w.res.Path, w.res.Doc.Span.StartLine,
		w.res.Doc.Span.EndLine), Text: "doc " + after.DocState, Doc: firstSentence(after.DocText),
		Extra: map[string]any{"state": after.DocState, "doc": after.DocText, "created": w.res.Created}}, nil
}

// reindex re-extracts files of a repo now, then refreshes the repo's
// freshness for the response header.
func (c *Ctx) reindex(r *config.Repo, paths ...string) error {
	if err := index.Files(c.Store, r, c.indexOptions(0), paths); err != nil {
		return err
	}
	delete(c.Fresh, r.Name)
	return c.EnsureFresh(r)
}

// indexOptions are the index options of the workspace with a limit on
// the files a freshness check re-extracts and the context's embedder.
func (c *Ctx) indexOptions(maxFiles int) index.Options {
	return index.Options{Workspace: c.Workspace.Name, Exclude: c.Workspace.Exclude, MaxFiles: maxFiles,
		RepoOrder: c.Workspace.RepoNames(), Embedder: c.Embedder}
}

// reviewed marks the doc of the symbol with a stable key as reviewed
// against its body (see store.MarkDocReviewed), under the write lock,
// and returns the symbol.
func (c *Ctx) reviewed(key string) (sy store.Symbol, err error) {
	unlock, err := c.Store.WriteLock()
	if err != nil {
		return sy, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	if sy, err = c.Store.SymbolByKey(key); err != nil {
		return sy, err
	}
	if err := c.Store.MarkDocReviewed(sy.ID); err != nil {
		return sy, err
	}
	return c.Store.SymbolByKey(key)
}

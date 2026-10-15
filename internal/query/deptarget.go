package query

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/deps"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/stablekey"
	"github.com/muralx/codedoc/internal/store"
)

// ErrDependency reports a dependency symbol given to a command that
// works on workspace symbols: dependencies are read-only, and have no
// callees, tests, history or docs of the workspace's own.
var ErrDependency = errors.New("is a dependency symbol; this command works on workspace symbols only")

// depMode says when a symbol address is looked up among the
// dependencies. An address that names a dependency itself (a stable key
// starting with a package id, a path inside a package directory) is
// always recognized.
type depMode int

const (
	// depsRefused: a dependency symbol is an error (ErrDependency), and
	// a name is never looked up among the dependencies.
	depsRefused depMode = iota
	// depsOnFlag: a name is looked up in the dependencies with --deps,
	// and without it when no workspace symbol has the name (efficiency
	// spec section 5): the answer is about the dependency symbol.
	depsOnFlag
	// depsElse: the same lookup for the queries that answer with the
	// workspace code using the dependency symbol (callers and refs).
	depsElse
)

// target is the symbol an address names: of the workspace (sym), or of
// a dependency (dep).
type target struct {
	sym store.Symbol
	dep *depSym
}

// isDepKey reports whether a stable key starts with a package id.
func isDepKey(key string) bool {
	_, ok := deps.ParseID(stablekey.Repo(key))
	return ok
}

// pick finds the workspace and dependency symbols an address names,
// after the freshness check of the repos it searches.
func (c *Ctx) pick(addr string, mode depMode) ([]store.Symbol, []depSym, []*config.Repo, error) {
	a := ParseAddr(addr)
	if a.Kind == AddrKey && isDepKey(a.Key) {
		found, repos, err := c.pickDepKey(a.Key)
		return nil, found, repos, err
	}
	syms, repos, err := c.resolve(a)
	if err != nil {
		return nil, nil, nil, err
	}
	found, err := c.pickDeps(a, len(syms) == 0, mode)
	return syms, found, repos, err
}

// pickDeps finds the dependency symbols an address that is no dependency
// key names: in a file of a dependency when the workspace has no such
// file (none says so), by name when mode allows it and --deps asks for
// it or no workspace symbol has the name.
func (c *Ctx) pickDeps(a Addr, none bool, mode depMode) ([]depSym, error) {
	switch {
	case a.Kind == AddrPath && none:
		return c.depInFile(a)
	case a.Kind == AddrLine && none:
		return c.depAtLine(a)
	case a.Kind == AddrBare && mode != depsRefused && (c.Deps || none):
		return c.deps().match(a.Name, "")
	}
	return nil, nil
}

// pickDepKey finds the dependency symbol with a stable key among the
// packages the query sees.
func (c *Ctx) pickDepKey(key string) ([]depSym, []*config.Repo, error) {
	repos := c.scope()
	if err := c.EnsureFresh(repos...); err != nil {
		return nil, nil, err
	}
	d, ok, err := c.deps().byKey(key)
	if err != nil || !ok {
		return nil, repos, err
	}
	return []depSym{d}, repos, nil
}

// depInFile finds the symbols a path form names in a file of a
// dependency (see depFileAt).
func (c *Ctx) depInFile(a Addr) ([]depSym, error) {
	target := a.Path
	if a.Repo != "" {
		target = a.Repo + ":" + a.Path
	}
	f, ok, err := c.depFileAt(target)
	if err != nil || !ok {
		return nil, err
	}
	syms, err := c.depFileSymbols(f)
	var out []depSym
	for _, d := range syms {
		if matchQualified(d.Qualified, a.Name) {
			out = append(out, d)
		}
	}
	return out, err
}

// depAtLine finds the innermost symbol enclosing a line of a file of a
// dependency (see depFileAt): the address path:LINE, tried when the
// workspace has no such file. The file found replaces the note that
// says so: by nothing when a symbol encloses the line, else by the note
// a workspace file gets (see noSymbolAt).
func (c *Ctx) depAtLine(a Addr) ([]depSym, error) {
	f, ok, err := c.depFileAt(a.Path)
	if err != nil || !ok {
		return nil, err
	}
	syms, err := c.depFileSymbols(f)
	if err != nil {
		return nil, err
	}
	best := -1
	for i, d := range syms {
		if d.StartLine <= a.Line && a.Line <= d.EndLine &&
			(best < 0 || d.EndLine-d.StartLine <= syms[best].EndLine-syms[best].StartLine) {
			best = i
		}
	}
	c.notes = nil
	if best < 0 {
		c.notes = append(c.notes, noSymbolAt(a.Line, f.file, f.abs))
		return nil, nil
	}
	return syms[best : best+1], nil
}

// depFile is an indexed file of a dependency: where it is on disk, its
// package and its row in deps.db.
type depFile struct {
	abs  string
	pkg  *depPkg
	file store.File
}

// depFileAt resolves a path to an indexed file of a package the query
// sees: repo:path, an absolute path, or a path relative to the working
// directory or to the current repo, lying in a directory the package is
// installed in and contained as every path argument is (see contained).
// Every query that takes a file of a dependency resolves it here; ok is
// false when the path names none.
func (c *Ctx) depFileAt(target string) (depFile, bool, error) {
	v := c.deps()
	for _, abs := range c.depCandidates(target) {
		p, rel, ok := v.located(abs)
		if !ok || !c.contained(abs) {
			continue
		}
		f, err := v.s.FileByPath(p.repoID, rel)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		return depFile{abs: abs, pkg: p, file: f}, err == nil, err
	}
	return depFile{}, false, nil
}

// depCandidates returns the absolute paths a dependency file address
// may mean: itself when absolute, below the named repo for repo:path,
// else below the working directory and the current repo.
func (c *Ctx) depCandidates(target string) []string {
	if filepath.IsAbs(target) {
		return []string{filepath.Clean(target)}
	}
	var bases []string
	p := target
	if repo, rest, ok := strings.Cut(target, ":"); ok {
		r, err := c.repoNamed(repo)
		if err != nil {
			return nil
		}
		bases, p = []string{r.Path}, rest
	} else {
		bases = append(bases, c.Cwd)
		if c.Repo != nil {
			bases = append(bases, c.Repo.Path)
		}
	}
	var out []string
	for _, b := range bases {
		if b != "" {
			out = append(out, filepath.Join(b, filepath.FromSlash(p)))
		}
	}
	return out
}

// depFileSymbols returns the symbols of a file of a dependency, in
// line order.
func (c *Ctx) depFileSymbols(f depFile) ([]depSym, error) {
	syms, err := c.view.s.SymbolsInFile(f.file.ID)
	return c.view.wrap(syms), err
}

// target resolves the address of a required symbol argument among the
// workspace symbols and, as mode allows, the dependency symbols. An
// address that names nothing is answered for its only plausible
// candidate, or with the nearest ones and ErrNearest (see unmatched);
// one that names several is answered for the best of them, the others
// noted (see best). When resolution is strict (--strict, a write) no
// match returns ErrNotFound and several ErrAmbiguous, each with a
// response (the candidates listed) to print before the error.
func (c *Ctx) target(addr string, mode depMode) (target, *output.Response, error) {
	syms, found, repos, err := c.pick(addr, mode)
	if err != nil {
		return target{}, nil, err
	}
	if mode == depsRefused && len(found) > 0 {
		return target{}, nil, fmt.Errorf("symbol %s: %w", addr, ErrDependency)
	}
	if c.noBest() {
		syms = preferExact(syms, addr)
	} else if syms, err = c.withNamesakes(addr, syms); err != nil {
		return target{}, nil, err
	}
	switch {
	case len(syms)+len(found) == 0:
		return c.unmatched(addr, repos, mode)
	case len(syms) == 1 && len(found) == 0:
		return target{sym: syms[0]}, nil, nil
	case len(syms) == 0 && len(found) == 1:
		return target{dep: &found[0]}, nil, nil
	case !c.noBest():
		t, err := c.best(addr, syms, found)
		return t, nil, err
	}
	return target{}, c.respond(repos, c.section("candidates", candidateEntries(syms, found))),
		fmt.Errorf("symbol %s: %w (%d candidates)", addr, ErrAmbiguous, len(syms)+len(found))
}

// candidateEntries lists the candidates of an ambiguous address:
// production code, tests, then dependency symbols.
func candidateEntries(syms []store.Symbol, found []depSym) []output.Entry {
	entries := make([]output.Entry, len(syms))
	for i, sy := range syms {
		entries[i] = symbolEntry(sy)
		if isDocSymbol(sy) && sy.Qualified != "" {
			// The heading path tells two sections of one name apart; their
			// first paragraph adds nothing to it.
			entries[i].Name, entries[i].Hide = sy.Qualified, output.HideDoc
		}
	}
	return append(testsLast(entries), depEntries(found)...)
}

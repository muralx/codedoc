package query

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/extract/doc"
	"github.com/muralx/codedoc/internal/parse"
	"github.com/muralx/codedoc/internal/stablekey"
	"github.com/muralx/codedoc/internal/store"
)

// AddrKind is the form of a symbol address.
type AddrKind int

// Symbol address forms (spec 5.1).
const (
	AddrBare AddrKind = iota // qualified name or name: MemStore.Get, Get
	AddrKey                  // full stable key
	AddrPath                 // [repo:]path#Qualified, or [repo:]doc.md#Heading
	AddrLine                 // [repo:]path:LINE
)

// lineAddr matches the address of a line: a path, a colon and a line
// number.
var lineAddr = regexp.MustCompile(`^(.*[^:]):([0-9]+)$`)

// Addr is a parsed symbol address.
type Addr struct {
	Kind AddrKind
	Key  string // AddrKey
	Repo string // AddrPath; empty means the current repo
	Path string // AddrPath; AddrLine: the path with its repo prefix, if any
	Name string // AddrPath and AddrBare: the qualified name or name
	Line int    // AddrLine
}

// ParseAddr classifies an address: at least three colons with a known
// language in the second segment and a non-empty qualified part is a
// stable key; else a string with '#'
// is a path form (one colon at most before the '#'); anything else is a
// bare name, so Rust's Type::method stays bare. A '#' right after a dot
// starts a TypeScript private member (Base.#secret), not a path form.
// Without a '#', a string ending in a colon and a line number is a line
// address (store/store.go:26).
func ParseAddr(s string) Addr {
	if stablekey.IsKey(s) {
		return Addr{Kind: AddrKey, Key: s}
	}
	if m := lineAddr.FindStringSubmatch(s); m != nil && !strings.Contains(s, "#") {
		if n, err := strconv.Atoi(m[2]); err == nil {
			return Addr{Kind: AddrLine, Path: m[1], Line: n}
		}
	}
	before, name, ok := strings.Cut(s, "#")
	if ok && strings.Count(before, ":") <= 1 && before != "" && !strings.HasSuffix(before, ".") {
		a := Addr{Kind: AddrPath, Path: before, Name: name}
		if repo, p, ok := strings.Cut(before, ":"); ok {
			a.Repo, a.Path = repo, p
		}
		return a
	}
	return Addr{Kind: AddrBare, Name: s}
}

// resolve finds the symbols an address names, after making the repos it
// searches fresh, and returns them with those repos.
func (c *Ctx) resolve(a Addr) ([]store.Symbol, []*config.Repo, error) {
	switch a.Kind {
	case AddrKey:
		return c.resolveKey(a.Key)
	case AddrPath:
		return c.resolvePath(a)
	case AddrLine:
		return c.resolveLine(a)
	default:
		if syms, repos, err := c.documentAt(a.Name); err != nil || len(syms) > 0 {
			return syms, repos, err
		}
		repos := c.scope()
		if err := c.EnsureFresh(repos...); err != nil {
			return nil, nil, err
		}
		l, err := c.workspace()
		if err != nil {
			return nil, nil, err
		}
		// Every symbol the name names: target keeps the exact ones when
		// resolution is strict (see preferExact) and ranks them otherwise.
		syms, err := l.everyNamed(a.Name, "")
		return syms, repos, err
	}
}

// resolveKey looks up a stable key; a key that does not parse or names
// an unknown repo is not found.
func (c *Ctx) resolveKey(key string) ([]store.Symbol, []*config.Repo, error) {
	k, err := stablekey.Parse(key)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", err.Error(), ErrNotFound)
	}
	r, err := c.repoNamed(k.Repo)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", err.Error(), ErrNotFound)
	}
	repos := []*config.Repo{r}
	if err := c.EnsureFresh(r); err != nil {
		return nil, nil, err
	}
	sy, err := c.Store.SymbolByKey(key)
	if errors.Is(err, store.ErrNotFound) {
		return nil, repos, nil
	}
	return []store.Symbol{sy}, repos, err
}

// resolvePath looks the name up in one file of the named repo, the
// current repo, or, without either, every repo of the workspace. In a
// document the name is a heading (see matchHeading).
func (c *Ctx) resolvePath(a Addr) ([]store.Symbol, []*config.Repo, error) {
	repos := c.Workspace.Repos
	switch {
	case a.Repo != "":
		r, err := c.repoNamed(a.Repo)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", err.Error(), ErrNotFound)
		}
		repos = []*config.Repo{r}
	case c.Repo != nil:
		repos = []*config.Repo{c.Repo}
	}
	if err := c.EnsureFresh(repos...); err != nil {
		return nil, nil, err
	}
	var out []store.Symbol
	for _, r := range repos {
		syms, err := c.inFile(r, a.Path)
		if err != nil {
			return nil, nil, err
		}
		if len(syms) > 0 && parse.IsDoc(syms[0].Lang) {
			out = append(out, matchHeading(syms, a.Name)...)
			continue
		}
		for _, sy := range syms {
			if matchQualified(sy.Qualified, a.Name) {
				out = append(out, sy)
			}
		}
	}
	return out, repos, nil
}

// inFile returns the symbols of one file, none when it is not indexed.
func (c *Ctx) inFile(r *config.Repo, p string) ([]store.Symbol, error) {
	id, err := c.repoID(r)
	if err != nil || id == 0 {
		return nil, err
	}
	f, err := c.Store.FileByPath(id, p)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return c.Store.SymbolsInFile(f.ID)
}

// lookup finds symbols by name in one store: the workspace index limited
// to a repo (0 for all), or deps.db limited to the package versions keep
// accepts (nil keeps every symbol).
type lookup struct {
	s      *store.Store
	repoID int64
	keep   func(store.Symbol) bool
	// prefer, when set, ranks the candidates of one nearness: lower
	// first (the repo and directory the query runs in, see cwdRank).
	prefer func(store.Symbol) int
	// repos are the repo rows keep accepts, when it is set.
	repos []int64
}

// workspace is the lookup over the workspace index under the --repo
// filter.
func (c *Ctx) workspace() (lookup, error) {
	repoID, err := c.filterID()
	return lookup{s: c.Store, repoID: repoID, prefer: c.cwdRank}, err
}

// named returns the symbols with a name the lookup covers.
func (l lookup) named(name, kind string) ([]store.Symbol, error) {
	syms, err := l.s.SymbolsByName(name, kind, l.repoID)
	if err != nil || l.keep == nil {
		return syms, err
	}
	return l.kept(syms), nil
}

// kept returns the symbols of syms that keep accepts.
func (l lookup) kept(syms []store.Symbol) []store.Symbol {
	var out []store.Symbol
	for _, sy := range syms {
		if l.keep(sy) {
			out = append(out, sy)
		}
	}
	return out
}

// byName finds symbols by a qualified name or name (see everyNamed),
// keeping those whose whole qualified name it is when there are any (see
// preferExact).
func (l lookup) byName(name, kind string) ([]store.Symbol, error) {
	syms, err := l.everyNamed(name, kind)
	return preferExact(syms, name), err
}

// everyNamed finds symbols by a qualified name or name: the last dot
// segment after the last slash (a TypeScript file prefix) is looked up
// by name and the whole must match the end of the qualified name. A name
// whose own text holds dots (a test title) is tried whole when the split
// finds nothing.
func (l lookup) everyNamed(name, kind string) ([]store.Symbol, error) {
	last := name[strings.LastIndex(name, "/")+1:]
	last = last[strings.LastIndex(last, ".")+1:]
	syms, err := l.named(last, kind)
	if err != nil {
		return nil, err
	}
	var out []store.Symbol
	for _, sy := range syms {
		if matchQualified(sy.Qualified, name) {
			out = append(out, sy)
		}
	}
	if len(out) == 0 && strings.Contains(name, ".") {
		if out, err = l.byPackageName(name, kind); err != nil || len(out) > 0 {
			return out, err
		}
	}
	if len(out) == 0 && last != name {
		return l.named(name, kind)
	}
	return out, nil
}

// match finds the symbols a find pattern names: a name (see byName) or a
// glob. With --kind section or document a pattern without glob
// characters matches any heading (or document name or title) containing
// it, ignoring case: headings are prose, seldom known word for word.
func (l lookup) match(pattern, kind string) ([]store.Symbol, error) {
	glob := strings.ContainsAny(pattern, "*?[")
	if !glob && !extract.IsDocKind(kind) {
		return l.byName(pattern, kind)
	}
	find := l.s.SymbolsByGlob
	if !glob {
		find = l.s.SymbolsLike
	}
	syms, err := find(pattern, kind, l.repoID)
	if err != nil || l.keep == nil {
		return syms, err
	}
	return l.kept(syms), nil
}

// preferExact keeps, of the symbols matching name, those whose qualified
// name is name itself (a TypeScript one after its file prefix) when there
// are any: a top-level Find wins over the members Store.Find and
// Cache.Find, while two top-level ones stay ambiguous.
func preferExact(syms []store.Symbol, name string) []store.Symbol {
	var exact []store.Symbol
	for _, sy := range syms {
		if sy.Qualified == name || strings.HasSuffix(sy.Qualified, "/"+name) {
			exact = append(exact, sy)
		}
	}
	if len(exact) == 0 || len(exact) == len(syms) {
		return syms
	}
	return exact
}

// byPackageName finds symbols by a Go-style address pkg.Name (or
// pkg.Type.Method): the qualified name after the first dot, in a module
// whose sources declare the name pkg (yaml for gopkg.in/yaml.v3) or
// whose identity ends with the segment pkg (its last path segment, or
// last dotted segment for Python and Java packages).
func (l lookup) byPackageName(name, kind string) ([]store.Symbol, error) {
	pkg, qualified, _ := strings.Cut(name, ".")
	syms, err := l.named(qualified[strings.LastIndex(qualified, ".")+1:], kind)
	if err != nil || len(syms) == 0 {
		return nil, err
	}
	ids := make([]int64, len(syms))
	for i, sy := range syms {
		ids[i] = sy.ModuleID
	}
	mods, err := l.s.ModulesByID(ids)
	if err != nil {
		return nil, err
	}
	var out []store.Symbol
	for _, sy := range syms {
		mod := mods[sy.ModuleID]
		if sy.Qualified == qualified && (mod.Name == pkg || lastSegment(mod.Identity) == pkg) {
			out = append(out, sy)
		}
	}
	return out, nil
}

// lastSegment is the last path segment of a module identity, then its
// last dotted segment: store for github.com/x/lib/store and for
// pylib.store.
func lastSegment(identity string) string {
	s := identity[strings.LastIndex(identity, "/")+1:]
	return s[strings.LastIndex(s, ".")+1:]
}

// matchQualified reports whether want names q: equal, or a suffix after a
// dot (container path), a slash (TypeScript file prefix) or the
// separator of a heading path (a section of a document).
func matchQualified(q, want string) bool {
	return q == want || strings.HasSuffix(q, "."+want) || strings.HasSuffix(q, "/"+want) ||
		strings.HasSuffix(q, doc.PathSep+want)
}

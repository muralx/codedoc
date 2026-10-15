package query

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/store"
)

// Siblings lists things of the same kind in the same place (spec 9). A
// target naming an indexed file gets the files with its name in sibling
// directories and the files of its directory sharing its suffix
// (*_store.go, *.test.ts); a directory gets the sibling directories whose
// files share basenames with it; anything else is a symbol address: the
// other implementations of the interfaces it (or, for a member, its
// type) implements, else the other members of its container with its
// kind. A config key, addressed config:KEY or by a name no symbol has,
// gets the other keys under the same parent path in the key's repos (see
// keySiblings).
func Siblings(c *Ctx, target string) (*output.Response, error) {
	if resp, ok, err := c.configAddrSiblings(target); ok || err != nil {
		return resp, err
	}
	if resp, ok, err := c.pathSiblings(target); ok || err != nil {
		return resp, err
	}
	sy, resp, err := c.graphTargetExact(target)
	if errors.Is(err, ErrNotFound) {
		if kresp, ok, kerr := c.keySiblings(target); ok || kerr != nil {
			return kresp, kerr
		}
		sy, resp, err = c.graphTarget(target)
	}
	if err != nil {
		return resp, err
	}
	secs, err := c.implSiblings(sy)
	if err != nil {
		return nil, err
	}
	if len(secs) == 0 {
		sec, err := c.memberSiblings(sy)
		if err != nil {
			return nil, err
		}
		secs = []output.Section{sec}
	}
	return c.respond(c.Workspace.Repos, secs...), nil
}

// configPrefix marks a siblings target as a config key. A key it names
// is looked up first; when there is none and a repo is named config, the
// target is that repo's path or symbol address.
const configPrefix = "config:"

// configAddrSiblings answers for a config:KEY target (see
// configPrefix); ok is false for any other target, or when no key
// matches and a repo is named config.
func (c *Ctx) configAddrSiblings(target string) (*output.Response, bool, error) {
	key, ok := strings.CutPrefix(target, configPrefix)
	if !ok {
		return nil, false, nil
	}
	if resp, ok, err := c.keySiblings(key); ok || err != nil {
		return resp, true, err
	}
	if _, err := c.repoNamed(strings.TrimSuffix(configPrefix, ":")); err == nil {
		return nil, false, nil
	}
	return nil, true, fmt.Errorf("config key %s: %w: no config file of the workspace defines it; codedoc config '*' lists the keys",
		key, ErrNotFound)
}

// keySiblings answers for a target naming a config key: the keys of the
// repos defining or using it that share its parent path (db.host:
// db.password; a key without a dot: the other keys without one); ok is
// false when no key has that name.
func (c *Ctx) keySiblings(name string) (*output.Response, bool, error) {
	repoID, err := c.filterID()
	if err != nil {
		return nil, false, err
	}
	keys, err := c.Store.ConfigKeysByName(name, repoID)
	if err != nil || len(keys) == 0 {
		return nil, false, err
	}
	parent := keyParent(name)
	sibs, repos, err := c.siblingKeys(name, keys)
	if err != nil {
		return nil, true, err
	}
	if err := c.EnsureFresh(repos...); err != nil {
		return nil, true, err
	}
	ku, err := c.configUses(sibs)
	if err != nil {
		return nil, true, err
	}
	sort.Strings(ku.names)
	title := "config keys under " + parent
	if parent == "" {
		title = "top-level config keys"
	}
	return c.respond(repos, c.tagged(title+" besides "+name, keyEntries(ku))), true, nil
}

// keyParent is the parent path of a config key: up to its last dot, ""
// for a key without one.
func keyParent(key string) string { return key[:max(strings.LastIndex(key, "."), 0)] }

// siblingKeys returns the keys sharing the parent path of name in the
// repos of keys (the rows named name), and those repos.
func (c *Ctx) siblingKeys(name string, keys []store.ConfigKey) ([]store.ConfigKey, []*config.Repo, error) {
	var sibs []store.ConfigKey
	var repos []*config.Repo
	for _, k := range keys {
		r, err := c.repoNamed(k.Repo)
		if err != nil {
			continue
		}
		repos = append(repos, r)
		all, err := c.Store.ConfigKeysMatching("*", k.RepoID)
		if err != nil {
			return nil, nil, err
		}
		for _, o := range all {
			if o.Key != name && keyParent(o.Key) == keyParent(name) {
				sibs = append(sibs, o)
			}
		}
	}
	return sibs, repos, nil
}

// pathSiblings answers for a target naming an indexed file or directory;
// ok is false when it names neither.
func (c *Ctx) pathSiblings(target string) (*output.Response, bool, error) {
	r, p, err := c.splitTarget(target)
	if err != nil || strings.Contains(target, "#") {
		return nil, false, nil
	}
	if err := c.EnsureFresh(r); err != nil {
		return nil, true, err
	}
	id, err := c.repoID(r)
	if err != nil || id == 0 {
		return nil, err != nil, err
	}
	files, err := c.Store.FilesByRepo(id)
	if err != nil {
		return nil, true, err
	}
	var secs []output.Section
	switch {
	case hasFile(files, p):
		secs = c.fileSiblings(files, p)
	case p != "." && hasDir(files, p):
		secs = []output.Section{c.dirSiblings(files, p)}
	default:
		return nil, false, nil
	}
	return c.respond([]*config.Repo{r}, secs...), true, nil
}

func hasFile(files []store.File, p string) bool {
	for _, f := range files {
		if f.Path == p {
			return true
		}
	}
	return false
}

func hasDir(files []store.File, dir string) bool {
	for _, f := range files {
		if strings.HasPrefix(f.Path, dir+"/") {
			return true
		}
	}
	return false
}

// fileSiblings lists the files named like p in sibling directories, then
// the files of p's directory sharing its suffix, test files last in each.
func (c *Ctx) fileSiblings(files []store.File, p string) []output.Section {
	dir, base := path.Dir(p), path.Base(p)
	suffix := suffixOf(base)
	var named, suffixed []output.Entry
	for _, f := range files {
		fd, fb := path.Dir(f.Path), path.Base(f.Path)
		switch {
		case f.Path == p:
		case dir != "." && fd != dir && fd != "." && path.Dir(fd) == path.Dir(dir) && fb == base:
			named = append(named, fileEntry(f))
		case fd == dir && suffix != "" && strings.HasSuffix(fb, suffix):
			suffixed = append(suffixed, fileEntry(f))
		}
	}
	secs := []output.Section{c.section(base+" in sibling directories", testsLast(named))}
	if suffix != "" {
		secs = append(secs, c.section("*"+suffix+" in "+dir, testsLast(suffixed)))
	}
	return secs
}

func fileEntry(f store.File) output.Entry {
	return output.Entry{Loc: f.Path, Text: fmt.Sprintf("%d lines", f.Lines), Extra: map[string]any{"lang": f.Lang},
		Tags: pathTestTags(f.Path)}
}

// suffixOf returns the part of a file name that marks its role: from the
// first dot of a name with several (records.test.ts: .test.ts), else from
// the last underscore or hyphen (user_store.go: _store.go); "" for none.
func suffixOf(base string) string {
	if strings.Count(base, ".") >= 2 {
		return base[strings.Index(base, "."):]
	}
	stem := strings.TrimSuffix(base, path.Ext(base))
	if i := strings.LastIndexAny(stem, "_-"); i > 0 {
		return base[i:]
	}
	return ""
}

// dirSiblings lists the directories next to dir that hold files with
// the same names as files directly in dir, most shared names first.
func (c *Ctx) dirSiblings(files []store.File, dir string) output.Section {
	parent := path.Dir(dir)
	mine := map[string]bool{}
	shared := map[string][]string{}
	for _, f := range files {
		if path.Dir(f.Path) == dir {
			mine[path.Base(f.Path)] = true
		}
	}
	for _, f := range files {
		fd := path.Dir(f.Path)
		if fd != dir && fd != "." && path.Dir(fd) == parent && mine[path.Base(f.Path)] {
			shared[fd] = append(shared[fd], path.Base(f.Path))
		}
	}
	dirs := make([]string, 0, len(shared))
	for d := range shared {
		dirs = append(dirs, d)
	}
	sort.Slice(dirs, func(i, j int) bool {
		if len(shared[dirs[i]]) != len(shared[dirs[j]]) {
			return len(shared[dirs[i]]) > len(shared[dirs[j]])
		}
		return dirs[i] < dirs[j]
	})
	entries := make([]output.Entry, len(dirs))
	for i, d := range dirs {
		sort.Strings(shared[d])
		entries[i] = output.Entry{Loc: d + "/", Text: fmt.Sprintf("%d shared: %s", len(shared[d]), strings.Join(shared[d], ", "))}
	}
	return c.section("directories next to "+dir+" sharing file names", entries)
}

// containerOf returns the qualified name of the container of a qualified
// name: up to its last dot after any TypeScript file prefix, "" at top
// level.
func containerOf(q string) string {
	slash := strings.LastIndex(q, "/")
	if dot := strings.LastIndex(q, "."); dot > slash {
		return q[:dot]
	}
	return ""
}

// memberSiblings lists the other symbols of sy's kind in its container,
// or at top level of its module.
func (c *Ctx) memberSiblings(sy store.Symbol) (output.Section, error) {
	syms, err := c.Store.SymbolsInModule(sy.ModuleID)
	if err != nil {
		return output.Section{}, err
	}
	container := containerOf(sy.Qualified)
	var entries []output.Entry
	for _, o := range syms {
		if o.ID != sy.ID && o.Kind == sy.Kind && containerOf(o.Qualified) == container {
			entries = append(entries, symbolEntry(o))
		}
	}
	where := "in " + moduleOf(sy)
	if container != "" {
		where = "of " + container
	}
	return c.section(fmt.Sprintf("other %s symbols %s", sy.Kind, where), testsLast(entries)), nil
}

// implSiblings lists, per interface the type of sy implements, the other
// implementing types, or for a member their member of the same name.
func (c *Ctx) implSiblings(sy store.Symbol) ([]output.Section, error) {
	typ, member, err := c.typeOf(sy)
	if err != nil || typ.ID == 0 {
		return nil, err
	}
	ifaces, err := c.Store.EdgesFrom(typ.ID, "implements")
	if err != nil {
		return nil, err
	}
	var secs []output.Section
	for _, in := range ifaces {
		if in.Dst == 0 {
			continue
		}
		entries, err := c.otherImpls(in.Dst, typ, member)
		if err != nil {
			return nil, err
		}
		iface, err := c.Store.SymbolByID(in.Dst)
		if err != nil {
			return nil, err
		}
		secs = append(secs, c.titled(about("other implementations of ", iface), entries))
	}
	return secs, nil
}

// typeOf returns sy when it is a type, else the type it is a member of
// (same module, its container's qualified name) and the member's name.
func (c *Ctx) typeOf(sy store.Symbol) (typ store.Symbol, member string, err error) {
	if sy.Kind == "struct" || sy.Kind == "class" {
		return sy, "", nil
	}
	container := containerOf(sy.Qualified)
	if container == "" {
		return store.Symbol{}, "", nil
	}
	typ, err = c.inModule(sy.ModuleID, container)
	return typ, sy.Name, err
}

// inModule returns the symbol of a module with a qualified name, or the
// zero Symbol when there is none.
func (c *Ctx) inModule(moduleID int64, qualified string) (store.Symbol, error) {
	name := qualified[strings.LastIndexAny(qualified, "./")+1:]
	syms, err := c.Store.SymbolsByName(name, "", 0)
	if err != nil {
		return store.Symbol{}, err
	}
	for _, s := range syms {
		if s.ModuleID == moduleID && s.Qualified == qualified {
			return s, nil
		}
	}
	return store.Symbol{}, nil
}

// otherImpls lists the implementers of iface other than typ, or with a
// member name, their member of that name.
func (c *Ctx) otherImpls(iface int64, typ store.Symbol, member string) ([]output.Entry, error) {
	edges, err := c.Store.EdgesTo(iface, "implements")
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(edges))
	for i, e := range edges {
		ids[i] = e.Src
	}
	srcs, err := c.Store.SymbolsByID(ids)
	if err != nil {
		return nil, err
	}
	var out []output.Entry
	for _, e := range edges {
		other, ok := srcs[e.Src]
		if e.Src == typ.ID || !ok {
			continue
		}
		if member != "" {
			if other, err = c.inModule(other.ModuleID, other.Qualified+"."+member); err != nil {
				return nil, err
			}
			if other.ID == 0 {
				continue
			}
		}
		out = append(out, symbolEntry(other))
	}
	return testsLast(out), nil
}

package resolve

import (
	"path"
	"strings"

	"github.com/muralx/codedoc/internal/store"
)

// pyImport resolves a Python import (spec 7.3). A relative import
// (.store, ..pkg) names a module of the importing repo relative to the
// package of the importing file. An absolute one resolves to the module
// named by its longest dotted prefix that any repo holds (a.b.c tries
// a.b.c, then a.b, then a), the importing repo first, then the others in
// config order.
func (r *resolver) pyImport(imp store.Import) (int64, error) {
	if !strings.HasPrefix(imp.Raw, ".") {
		return r.longestPrefix("py", prefixes(imp.Raw, 0), imp.RepoID)
	}
	pkg, err := r.moduleRowAt(imp.RepoID, "py", path.Dir(imp.Path))
	if err != nil {
		return 0, err
	}
	target, ok := relativeModule(pkg.Identity, imp.Raw)
	if !ok {
		return 0, nil
	}
	for _, id := range prefixes(target, 0) {
		if m, err := r.moduleByIdentity(imp.RepoID, "py", id); m.ID != 0 || err != nil {
			return m.ID, err
		}
	}
	return 0, nil
}

// relativeModule resolves a relative module name against the dotted name
// of a package ("" for the top level): one dot is the package itself,
// each further dot its parent. It reports false when the dots climb above
// the top level.
func relativeModule(pkg, raw string) (string, bool) {
	rest := strings.TrimLeft(raw, ".")
	up := len(raw) - len(rest) - 1
	segs := splitDotted(pkg)
	if up > len(segs) {
		return "", false
	}
	segs = append(segs[:len(segs)-up], splitDotted(rest)...)
	return strings.Join(segs, "."), len(segs) > 0
}

// javaImport resolves a Java import: import a.b.C names the package a.b
// (static imports a.b.C.m climb further), import a.b.* the package a.b.
// The longest such prefix that any repo holds wins, the importing repo
// first, then the others in config order.
func (r *resolver) javaImport(imp store.Import) (int64, error) {
	raw, drop := strings.TrimSuffix(imp.Raw, ".*"), 1
	if raw != imp.Raw {
		drop = 0
	}
	return r.longestPrefix("java", prefixes(raw, drop), imp.RepoID)
}

// longestPrefix returns the module of the first identity in ids that a
// repo holds, preferring the importing repo, then config order.
func (r *resolver) longestPrefix(lang string, ids []string, from int64) (int64, error) {
	for _, id := range ids {
		repos, err := r.holding(lang, id, from)
		if err != nil {
			return 0, err
		}
		for _, repo := range repos {
			if m, err := r.moduleByIdentity(repo.ID, lang, id); m.ID != 0 || err != nil {
				return m.ID, err
			}
		}
	}
	return 0, nil
}

// prefixes returns the dotted prefixes of name, longest first, without
// its last drop segments.
func prefixes(name string, drop int) []string {
	segs := splitDotted(name)
	var out []string
	for i := len(segs) - drop; i > 0; i-- {
		out = append(out, strings.Join(segs[:i], "."))
	}
	return out
}

func splitDotted(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ".")
}

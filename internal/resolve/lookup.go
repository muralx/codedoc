package resolve

import (
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/muralx/codedoc/internal/store"
)

// tsExtensions are the file extensions a TS/JS specifier without one may
// name, in the order TypeScript tries them.
var tsExtensions = []string{".ts", ".tsx", ".d.ts", ".js", ".jsx", ".mts", ".cts", ".mjs", ".cjs"}

// resolver resolves imports through one batch. Lookups are cached; after
// preload the module and provider maps are complete, so a miss is final
// and costs no query.
type resolver struct {
	b          *store.Batch
	rank       map[string]int
	complete   bool
	providers  map[string][]store.Repo // lang, identity
	byIdentity map[string]store.Module // repo, lang, identity
	byPath     map[string]store.Module // repo, lang, path
	files      map[string]bool         // repo, stem
	holders    map[string][]store.Repo // lang, identity: repos holding such a module
}

func newResolver(b *store.Batch, order []string) *resolver {
	rank := make(map[string]int, len(order))
	for i, name := range order {
		rank[name] = i
	}
	return &resolver{b: b, rank: rank, providers: map[string][]store.Repo{},
		byIdentity: map[string]store.Module{}, byPath: map[string]store.Module{}, files: map[string]bool{},
		holders: map[string][]store.Repo{}}
}

func key(repoID int64, parts ...string) string {
	return strconv.FormatInt(repoID, 10) + "\x00" + strings.Join(parts, "\x00")
}

// preload reads every module and provided identity of the workspace.
func (r *resolver) preload() error {
	mods, err := r.b.Modules()
	if err != nil {
		return err
	}
	for _, m := range mods {
		r.byPath[key(m.RepoID, m.Lang, m.Path)] = m
		k := key(m.RepoID, m.Lang, m.Identity)
		if old, ok := r.byIdentity[k]; !ok || depthOf(m.Path) < depthOf(old.Path) {
			r.byIdentity[k] = m
		}
	}
	provs, err := r.b.Providers()
	if err != nil {
		return err
	}
	for _, p := range provs {
		k := p.Lang + "\x00" + p.Identity
		r.providers[k] = append(r.providers[k], p.Repo)
	}
	r.complete = true
	return nil
}

// moduleByIdentity returns the module of a repo with an identity (the
// shallowest when several share it), or the zero Module.
func (r *resolver) moduleByIdentity(repoID int64, lang, identity string) (store.Module, error) {
	k := key(repoID, lang, identity)
	if m, ok := r.byIdentity[k]; ok || r.complete {
		return m, nil
	}
	m, err := r.b.ModuleByIdentity(repoID, lang, identity)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return store.Module{}, err
	}
	r.byIdentity[k] = m
	return m, nil
}

// moduleAt returns the id of the module at a path, or 0.
func (r *resolver) moduleAt(repoID int64, lang, dir string) (int64, error) {
	m, err := r.moduleRowAt(repoID, lang, dir)
	return m.ID, err
}

// moduleRowAt returns the module at a path, or the zero Module.
func (r *resolver) moduleRowAt(repoID int64, lang, dir string) (store.Module, error) {
	k := key(repoID, lang, dir)
	if m, ok := r.byPath[k]; ok || r.complete {
		return m, nil
	}
	m, err := r.b.ModuleByPath(repoID, lang, dir)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return store.Module{}, err
	}
	r.byPath[k] = m
	return m, nil
}

// holding returns the repos holding a module of a language with an
// identity, the importing repo first, then in config order.
func (r *resolver) holding(lang, identity string, from int64) ([]store.Repo, error) {
	k := lang + "\x00" + identity
	repos, ok := r.holders[k]
	if !ok {
		var err error
		if repos, err = r.b.ReposWithModule(lang, identity); err != nil {
			return nil, err
		}
		r.holders[k] = repos
	}
	out := append([]store.Repo(nil), repos...)
	sort.SliceStable(out, func(i, j int) bool { return r.before(out[i], out[j], from) })
	return out, nil
}

// fileExists reports whether a repo holds stem with a TS or JS extension.
func (r *resolver) fileExists(repoID int64, stem string) (bool, error) {
	k := key(repoID, stem)
	if ok, cached := r.files[k]; cached {
		return ok, nil
	}
	paths := make([]string, len(tsExtensions))
	for i, ext := range tsExtensions {
		paths[i] = stem + ext
	}
	ok, err := r.b.AnyFile(repoID, paths)
	r.files[k] = ok
	return ok, err
}

// preferred returns the repos providing an identity, the importing repo
// first, then in config order (unknown names last, by id).
func (r *resolver) preferred(lang, identity string, from int64) ([]store.Repo, error) {
	k := lang + "\x00" + identity
	repos, ok := r.providers[k]
	if !ok && !r.complete {
		var err error
		if repos, err = r.b.ReposProviding(lang, identity); err != nil {
			return nil, err
		}
		r.providers[k] = repos
	}
	out := append([]store.Repo(nil), repos...)
	sort.SliceStable(out, func(i, j int) bool { return r.before(out[i], out[j], from) })
	return out, nil
}

func (r *resolver) before(a, b store.Repo, from int64) bool {
	if (a.ID == from) != (b.ID == from) {
		return a.ID == from
	}
	ra, oka := r.rank[a.Name]
	rb, okb := r.rank[b.Name]
	switch {
	case oka && okb:
		return ra < rb
	case oka != okb:
		return oka
	}
	return a.ID < b.ID
}

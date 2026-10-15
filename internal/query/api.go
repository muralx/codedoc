package query

import (
	"cmp"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/store"
)

// apiModules is the most modules of a package whose names an api answer
// lists besides its main module: the ones the workspace uses most.
// apiNames is the most names it lists of each module of a package; a
// module asked for by its import specifier, or continued with an
// offset, lists them all.
const (
	apiModules = 6
	apiNames   = 40
)

// APIOpts control the api query.
type APIOpts struct {
	// Doc prints the first doc sentence of every name listed.
	Doc bool
}

// API shows the surface of a third-party package of the workspace, no
// --deps needed. pkg is the name a manifest declares (also name@version
// or the package id), or an import specifier below it, which names one
// module. Without a name the answer is the package with the first
// sentence of its README, then the exported top-level names of its main
// module and of the modules the workspace uses most (see api.surface,
// at most apiNames of each), then the list of its modules. With a name it is that type, function
// or member with its doc, a type with its members, the inherited ones
// among them (see api.named). A name the package does not have lists
// the nearest exported names; a package that is declared but not
// indexed says why and what to do, in one line.
func API(c *Ctx, pkg, name string, o APIOpts) (*output.Response, error) {
	repos := c.scope()
	if err := c.EnsureFresh(repos...); err != nil {
		return nil, err
	}
	v := c.deps()
	a := &api{c: c, v: v, doc: o.Doc, target: strings.TrimSuffix(pkg, "/")}
	if err := a.locate(); err != nil {
		return nil, err
	}
	if a.pkg == nil {
		return a.missing()
	}
	var secs []output.Section
	var err error
	if name != "" {
		secs, err = a.named(name)
	} else {
		secs, err = a.surface()
	}
	if err != nil {
		return nil, err
	}
	return c.share(c.respond(repos, secs...)), nil
}

// api builds an api answer about the package target names: pkg, with
// module set when target is an import specifier of one of its modules,
// and root its main module.
type api struct {
	c      *Ctx
	v      *depView
	doc    bool
	target string
	pkg    *depPkg
	module *store.Module
	root   store.Module
	// used counts, per stable key of a symbol of the package, the edges
	// of the workspace that end at it.
	used map[string]int
}

// locate finds the package of the answer among the indexed ones: by
// name, name@version or id (the first version in id order when the
// workspace uses several, the others are noted), else by the import
// specifier of one of its modules.
func (a *api) locate() error {
	pkgs := a.v.named(a.target)
	if len(pkgs) == 0 && a.v != nil {
		for _, p := range a.v.pkgs {
			m, ok, err := a.v.moduleNamed(p, a.target)
			if err != nil {
				return err
			}
			if ok && strings.HasPrefix(a.target, p.id.Name+"/") {
				pkgs, a.module = append(pkgs, p), &m
				break
			}
		}
	}
	if len(pkgs) == 0 {
		return nil
	}
	a.pkg = pkgs[0]
	for _, other := range pkgs[1:] {
		a.c.notes = append(a.c.notes, fmt.Sprintf("the workspace also uses %s@%s: codedoc api %s", other.id.Name,
			other.id.Version, shellQuote(other.id.Name+"@"+other.id.Version)))
	}
	root, _, err := a.v.moduleNamed(a.pkg, a.pkg.id.String())
	if err != nil {
		return err
	}
	a.root, a.used = root, map[string]int{}
	uses, err := a.c.Store.DepKeysUsed(a.pkg.id.String(), nodeCap)
	for _, u := range uses {
		a.used[u.Key] = u.N
	}
	return err
}

// head is the line of the package: name@version, the directory it is
// installed in and the first sentence of its README.
func (a *api) head() output.Section {
	p := a.pkg
	return oneSymbol(output.Entry{Key: p.id.String(), Name: p.id.Name + "@" + p.id.Version, Loc: p.root(),
		Doc:   firstSentence(readmeLead(a.root.DocText)),
		Extra: map[string]any{"kind": "package", "dep": p.id.String(), "dir": p.root()}})
}

// apiModule is a module of the package with its exported top-level
// names and the uses the workspace makes of them and of their members.
type apiModule struct {
	store.Module
	names []store.Symbol
	uses  int
}

// modules reads the modules of the package that declare something, in
// the order codedoc outline lists them.
func (a *api) modules() ([]apiModule, error) {
	mods, err := a.v.s.ModulesByRepo(a.pkg.repoID)
	if err != nil {
		return nil, err
	}
	var out []apiModule
	for _, m := range mods {
		syms, err := a.v.s.SymbolsInModule(m.ID)
		if err != nil {
			return nil, err
		}
		if len(syms) == 0 && m.DocText == "" {
			continue
		}
		am := apiModule{Module: m}
		for _, sy := range syms {
			am.uses += a.used[sy.StableKey]
		}
		top, _ := fold(syms, false)
		seen := map[string]bool{}
		for _, sy := range top {
			// A name declared once per platform or overload is one name.
			if id := sy.Kind + " " + sy.Name + signatureTail(sy); sy.Visibility == "public" && !seen[id] {
				seen[id] = true
				am.names = append(am.names, sy)
			}
		}
		out = append(out, am)
	}
	return out, nil
}

// surface is the answer for a package: its line, the names of its main
// module, then of the apiModules modules the workspace uses most, and
// the list of all its modules (see index). An
// import specifier is answered with the names of that module alone, and
// so is an offset, which continues the names of the main module.
func (a *api) surface() ([]output.Section, error) {
	mods, err := a.modules()
	if err != nil {
		return nil, err
	}
	secs := []output.Section{a.head()}
	main := a.root.ID
	if a.module != nil {
		main = a.module.ID
	}
	var others []apiModule
	for _, m := range mods {
		switch {
		case len(m.names) == 0:
		case m.ID == main:
			secs = append(secs, a.names(m, a.module == nil))
		case m.uses > 0:
			others = append(others, m)
		}
	}
	if a.module != nil || a.c.Offset > 0 {
		return secs, nil
	}
	slices.SortStableFunc(others, func(x, y apiModule) int { return cmp.Compare(y.uses, x.uses) })
	for _, m := range others[:min(len(others), apiModules)] {
		secs = append(secs, a.names(m, true))
	}
	return append(secs, a.index(mods)...), nil
}

// index is the section listing the modules of the package, each with
// the number of names it exports, in the order of codedoc outline,
// which continues it; none for a package of one module. A directory of
// more than crowdedFile modules lists its first shownSites.
func (a *api) index(mods []apiModule) []output.Section {
	if len(mods) < 2 {
		return nil
	}
	entries := make([]output.Entry, len(mods))
	for i, m := range mods {
		entries[i] = output.Entry{Loc: m.Identity, Text: fmt.Sprintf("%d %s", len(m.names), plural(len(m.names), "name", "names")),
			Extra: map[string]any{"kind": "module", "names": len(m.names)}}
	}
	command := "codedoc outline " + shellQuote(a.pkg.id.Name+"@"+a.pkg.id.Version) + a.c.Scope
	crowded := crowd(entries, crowding{what: "modules in this directory", command: command, wide: true,
		dir: func(e output.Entry) string { return e.Loc[:strings.LastIndex(e.Loc, "/")+1] }})
	return []output.Section{{Title: "modules of " + a.pkg.id.Name, Entries: crowded, Dirs: true, Total: len(mods),
		Command: command}}
}

// names is the section of the exported top-level names of a module, the
// ones the workspace uses first, then types before functions before
// values, by name. With first, and without an offset, it lists the
// first apiNames of them; codedoc api <its import specifier> continues
// it.
func (a *api) names(m apiModule, first bool) output.Section {
	syms := slices.Clone(m.names)
	slices.SortStableFunc(syms, func(x, y store.Symbol) int {
		return cmp.Or(cmp.Compare(a.used[y.StableKey], a.used[x.StableKey]), cmp.Compare(kindRank(x.Kind), kindRank(y.Kind)),
			cmp.Compare(x.Name, y.Name))
	})
	entries := make([]output.Entry, len(syms))
	for i, sy := range syms {
		entries[i] = a.nameEntry(sy)
	}
	sec := a.c.section(m.Identity, entries)
	sec.Command = "codedoc api " + shellQuote(m.Identity) + a.c.Scope
	if first && a.c.Offset == 0 && len(entries) > apiNames {
		sec.Total, sec.Entries = len(entries), entries[:apiNames]
	}
	return sec
}

// kindRank orders the kinds of names: classes and other types that hold
// members, functions, then the rest.
func kindRank(kind string) int {
	switch {
	case kind == "class" || kind == "struct":
		return 0
	case kind == "function":
		return 1
	case memberKinds[kind]:
		return 2
	}
	return 3
}

// nameEntry is the line of an exported name: the name, its kind, for a
// function its signature from the name on, and the first sentence of
// its doc, which text prints only when asked.
func (a *api) nameEntry(sy store.Symbol) output.Entry {
	e := output.Entry{Key: sy.StableKey, Name: sy.Name, Text: sy.Kind, Doc: firstSentence(sy.DocText),
		Extra: map[string]any{"kind": sy.Kind, "dep": a.pkg.id.String(), "signature": sy.Signature,
			"loc": spanLoc(sy.Path, sy.StartLine, sy.EndLine)}}
	if sy.Kind == "function" || sy.Kind == "method" {
		e.Text += signatureTail(sy)
	}
	if !a.doc {
		e.Hide = output.HideDoc
	}
	return e
}

// signatureTail is what the signature of a symbol says after its name:
// the type parameters, the parameters and the result of a function, the
// type of a field. A signature the index holds only the first line of
// ends with an ellipsis; one that does not hold the name is returned
// whole after a space.
func signatureTail(sy store.Symbol) string {
	tail := " " + sy.Signature
	if i := strings.Index(sy.Signature, sy.Name); i >= 0 {
		tail = sy.Signature[i+len(sy.Name):]
	}
	tail = strings.TrimRight(tail, " ;")
	if strings.HasSuffix(tail, "{") || strings.HasSuffix(tail, "<") || strings.HasSuffix(tail, "(") || strings.HasSuffix(tail, ",") {
		tail += " ..."
	}
	return tail
}

// abs is the location of a symbol of the package on disk, or relative
// to the package when it is no longer installed.
func (a *api) abs(sy store.Symbol) string {
	p := sy.Path
	if root := a.pkg.root(); root != "" {
		p = filepath.Join(root, filepath.FromSlash(sy.Path))
	}
	return spanLoc(p, sy.StartLine, sy.EndLine)
}

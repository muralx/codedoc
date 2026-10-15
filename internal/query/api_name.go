package query

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/store"
)

// nearestNames is the most names an api answer suggests for a name the
// package does not have.
const nearestNames = 10

// named is the answer for a name of the package: the symbol with its
// location on disk, its signature and its whole doc, tagged with the
// package and the module to import it from; for a type, its members
// follow (see members). Type.member names a member, also one the type
// inherits. Of several symbols with the name the one the workspace uses
// most is answered and the others are noted. A name the package does
// not have is answered with the nearest exported names.
func (a *api) named(name string) ([]output.Section, error) {
	found, err := a.find(name)
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return a.nearest(name)
	}
	slices.SortStableFunc(found, func(x, y depSym) int {
		return cmp.Or(cmp.Compare(a.used[y.StableKey], a.used[x.StableKey]), cmp.Compare(x.ContainerID, y.ContainerID),
			cmp.Compare(len(x.Path), len(y.Path)), cmp.Compare(x.Path, y.Path))
	})
	d := found[0]
	if len(found) > 1 {
		var others []string
		for _, o := range found[1:] {
			others = append(others, fmt.Sprintf("%s:%d", o.Path, o.StartLine))
		}
		a.c.notes = append(a.c.notes, fmt.Sprintf("%d more named %s in %s: %s", len(others), name, a.pkg.id.Name,
			strings.Join(others, ", ")))
	}
	e := depDocEntry(d)
	e.Loc = a.abs(d.Symbol)
	e.Tags = append(e.Tags, "from "+moduleOf(d.Symbol))
	head := oneSymbol(e)
	head.AllTags = true
	if !memberKinds[d.Kind] {
		return []output.Section{head}, nil
	}
	members, err := a.members(d)
	return append([]output.Section{head}, members...), err
}

// find looks a name, a qualified name or Type.member up among the
// symbols of the package; a member a type inherits is found through it.
func (a *api) find(name string) ([]depSym, error) {
	l := lookup{s: a.v.s, repoID: a.pkg.repoID}
	syms, err := l.byName(name, "")
	if err != nil {
		return nil, err
	}
	found := a.v.wrap(syms)
	if len(found) > 0 {
		return found, nil
	}
	inherited, err := a.v.inherited(name, "")
	for _, d := range inherited {
		if d.pkg == a.pkg {
			found = append(found, d)
		}
	}
	return found, err
}

// members is the section of the members of a type: the ones it declares
// that are not private, by line, then the public ones its supertypes
// declare, nearest supertype first and at most inheritedCap, each named
// after the type that declares it (Protocol.request) under its file. A
// member is its name and its signature from the name on.
func (a *api) members(typ depSym) ([]output.Section, error) {
	own, err := a.v.members(typ.Symbol)
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	var entries []output.Entry
	for _, m := range own {
		names[m.Name] = true
		if m.Visibility != "private" {
			entries = append(entries, a.memberEntry(m, m.Name))
		}
	}
	inherited, err := a.inherited(typ, names)
	if err != nil {
		return nil, err
	}
	if entries = append(entries, inherited...); len(entries) == 0 {
		return nil, nil
	}
	return []output.Section{a.c.titled(about("members of ", typ.Symbol), entries)}, nil
}

// inherited lists the public members the supertypes of typ declare
// under names it does not declare itself (names holds those seen), the
// nearest supertype first, at most inheritedCap; the rest is noted.
func (a *api) inherited(typ depSym, names map[string]bool) ([]output.Entry, error) {
	supers, err := a.v.supers(typ.Symbol)
	if err != nil {
		return nil, err
	}
	var entries []output.Entry
	more := 0
	for _, sup := range supers {
		members, err := a.v.members(sup)
		if err != nil {
			return nil, err
		}
		for _, m := range members {
			if names[m.Name] || m.Visibility != "public" {
				continue
			}
			names[m.Name] = true
			if more++; more <= inheritedCap {
				entries = append(entries, a.memberEntry(m, sup.Name+"."+m.Name))
			}
		}
	}
	if more > inheritedCap {
		a.c.notes = append(a.c.notes, fmt.Sprintf("%d more inherited members not listed", more-inheritedCap))
	}
	return entries, nil
}

// memberEntry is the line of a member of a type under a name: where it
// is declared in the package, the modifiers its signature starts with
// (protected, static, get; not the func and receiver of a Go method)
// and what follows the name (its kind when the signature says nothing),
// and the first sentence of its doc, which text prints only when asked.
func (a *api) memberEntry(m store.Symbol, name string) output.Entry {
	text := signatureTail(m)
	if i := strings.Index(m.Signature, m.Name); i > 0 && !strings.HasPrefix(m.Signature, "func") {
		text = m.Signature[:i] + strings.TrimSpace(text)
	}
	e := output.Entry{Key: m.StableKey, Name: name, Loc: spanLoc(m.Path, m.StartLine, m.EndLine),
		Text: strings.TrimSpace(text), Doc: firstSentence(m.DocText),
		Extra: map[string]any{"kind": m.Kind, "dep": a.pkg.id.String(), "signature": m.Signature}}
	if e.Text == "" {
		e.Text = m.Kind
	}
	if !a.doc {
		e.Hide = output.HideDoc
	}
	return e
}

// nearest answers for a name the package does not have: the exported
// top-level names nearest to it (see rankNames: the same ignoring case,
// those ending with it, holding it or held by it, then a typo away),
// each with the module to import it from. Without any it is
// ErrNotFound.
func (a *api) nearest(name string) ([]output.Section, error) {
	mods, err := a.modules()
	if err != nil {
		return nil, err
	}
	byName := map[string][]output.Entry{}
	var names []string
	for _, m := range mods {
		for _, sy := range m.names {
			e := a.nameEntry(sy)
			e.Loc = m.Identity
			if byName[sy.Name] == nil {
				names = append(names, sy.Name)
			}
			byName[sy.Name] = append(byName[sy.Name], e)
		}
	}
	var entries []output.Entry
	for _, n := range rankNames(names, name[strings.LastIndex(name, ".")+1:]) {
		entries = append(entries, byName[n.name]...)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("%s in %s: %w: it exports no name like that; codedoc api %s lists what it exports",
			name, a.pkg.id.Name, ErrNotFound, shellQuote(a.pkg.id.Name))
	}
	a.c.notes = append(a.c.notes, fmt.Sprintf("%s exports no %s; the nearest names are listed", a.pkg.id.Name, name))
	return []output.Section{{Title: "nearest names in " + a.pkg.id.Name, Entries: entries[:min(len(entries), nearestNames)]}}, nil
}

// missing answers for a package no query can see: one line saying what
// the manifests of the workspace know about it (declared but imported
// nowhere, not installed, waiting, failed, a package of the workspace)
// and what to do. A name no manifest declares is ErrNotFound, one line
// with the nearest declared names or the command that lists them all.
func (a *api) missing() (*output.Response, error) {
	c := a.c
	repos := c.scope()
	uses, err := c.Store.DepUses(0)
	if err != nil {
		return nil, err
	}
	var declared, near []string
	for _, u := range uses {
		name := u.PackageName()
		if a.target == name || a.target == u.Name || a.target == u.PackageID() || a.target == name+"@"+u.Version {
			c.notes = append(c.notes, depStateLine(u))
			return c.respond(repos), nil
		}
		if !slices.Contains(declared, name) {
			declared = append(declared, name)
		}
	}
	for _, n := range rankNames(declared, a.target) {
		near = append(near, n.name)
	}
	hint := "codedoc deps lists the packages"
	if len(near) > 0 {
		hint = "nearest: " + strings.Join(near, ", ")
	}
	return nil, fmt.Errorf("package %s: %w: no manifest of the workspace declares it; %s", a.target, ErrNotFound, hint)
}

// depStateLine says why a declared package has no api answer and what
// to do about it.
func depStateLine(u store.DepUse) string {
	name := u.PackageName()
	if u.Version != "" {
		name += "@" + u.Version
	}
	switch u.State {
	case store.DepUnused:
		return fmt.Sprintf("%s is declared in %s but no source file imports it, so it is not indexed; "+
			"name it under deps.include in the codedoc config and run codedoc deps index", name, u.Manifest)
	case store.DepMissing:
		return fmt.Sprintf("%s is declared in %s but not installed; install the dependencies and run codedoc deps index",
			name, u.Manifest)
	case store.DepFailed:
		return fmt.Sprintf("indexing %s failed: %s; codedoc deps index tries again", name, u.Error)
	case store.DepSkipped:
		return fmt.Sprintf("%s is a package of the workspace, indexed as a repo: codedoc outline and codedoc find answer for it",
			name)
	}
	return fmt.Sprintf("%s is declared in %s and waits to be indexed; run codedoc deps index", name, u.Manifest)
}

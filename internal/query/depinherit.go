package query

import (
	"strings"

	"github.com/muralx/codedoc/internal/store"
)

// maxSupers bounds the supertypes followed from one dependency type.
const maxSupers = 16

// memberKinds are the kinds of symbols that hold members.
var memberKinds = map[string]bool{"class": true, "interface": true, "struct": true, "enum": true, "trait": true}

// supers returns the types a dependency type inherits from through the
// extends edges of deps.db, nearest first, each once.
func (v *depView) supers(typ store.Symbol) ([]store.Symbol, error) {
	seen := map[int64]bool{typ.ID: true}
	queue := []int64{typ.ID}
	var out []store.Symbol
	for len(queue) > 0 && len(out) < maxSupers {
		id := queue[0]
		queue = queue[1:]
		edges, err := v.s.EdgesFrom(id, "extends")
		if err != nil {
			return nil, err
		}
		for _, e := range edges {
			if seen[e.Dst] {
				continue
			}
			seen[e.Dst] = true
			sup, err := v.s.SymbolByID(e.Dst)
			if err != nil {
				return nil, err
			}
			out, queue = append(out, sup), append(queue, e.Dst)
		}
	}
	return out, nil
}

// members returns the symbols a dependency type declares itself, in
// line order.
func (v *depView) members(typ store.Symbol) ([]store.Symbol, error) {
	syms, err := v.s.SymbolsInFile(typ.FileID)
	if err != nil {
		return nil, err
	}
	var out []store.Symbol
	for _, sy := range syms {
		if sy.ContainerID == typ.ID {
			out = append(out, sy)
		}
	}
	return out, nil
}

// inherited resolves Type.member to the member a supertype of Type
// declares, for every dependency type the name before the last dot
// names; the result carries Type as via.
func (v *depView) inherited(name, kind string) ([]depSym, error) {
	dot := strings.LastIndex(name, ".")
	if dot <= strings.LastIndex(name, "/") {
		return nil, nil
	}
	types, err := v.lookup().byName(name[:dot], "")
	if err != nil {
		return nil, err
	}
	var out []depSym
	for _, typ := range types {
		if !memberKinds[typ.Kind] {
			continue
		}
		m, ok, err := v.inheritedMember(typ, name[dot+1:], kind)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, depSym{Symbol: m, pkg: v.byRepo[m.RepoID], via: typ.Name})
		}
	}
	return out, nil
}

// inheritedMember returns the member with a name that the nearest
// supertype of typ declares.
func (v *depView) inheritedMember(typ store.Symbol, name, kind string) (store.Symbol, bool, error) {
	supers, err := v.supers(typ)
	if err != nil {
		return store.Symbol{}, false, err
	}
	named, err := v.s.SymbolsByName(name, kind, typ.RepoID)
	if err != nil {
		return store.Symbol{}, false, err
	}
	for _, sup := range supers {
		for _, sy := range named {
			if sy.ContainerID == sup.ID {
				return sy, true, nil
			}
		}
	}
	return store.Symbol{}, false, nil
}

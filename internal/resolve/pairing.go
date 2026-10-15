package resolve

import "github.com/muralx/codedoc/internal/store"

// pairing picks the interface and struct pairs an implements run checks
// and computes interface method sets with embedded interfaces expanded.
type pairing struct {
	ifaces, structs []store.Target
	byID            map[int64]store.Target
	embeds          map[int64][]int64
	embeddedBy      map[int64][]int64
	partial         bool
	// scopeIfaces holds the interfaces of the scope modules and those
	// embedding them; scopeStructs the structs of the scope modules;
	// candidates the other interfaces a scope struct may implement.
	scopeIfaces, scopeStructs, candidates map[int64]bool
	want                                  map[int64]map[string]bool
}

func newPairing(ifaces, structs []store.Target, embeds map[int64][]int64, moduleIDs []int64) *pairing {
	p := &pairing{ifaces: ifaces, structs: structs, embeds: embeds, partial: moduleIDs != nil,
		byID: map[int64]store.Target{}, embeddedBy: map[int64][]int64{}, scopeIfaces: map[int64]bool{},
		scopeStructs: map[int64]bool{}, candidates: map[int64]bool{}, want: map[int64]map[string]bool{}}
	scope := map[int64]bool{}
	for _, id := range moduleIDs {
		scope[id] = true
	}
	for _, in := range ifaces {
		p.byID[in.ID] = in
		if scope[in.ModuleID] {
			p.scopeIfaces[in.ID] = true
		}
	}
	for src, dsts := range embeds {
		for _, d := range dsts {
			p.embeddedBy[d] = append(p.embeddedBy[d], src)
		}
	}
	p.closeOverEmbedders(p.scopeIfaces)
	for _, st := range structs {
		if scope[st.ModuleID] {
			p.scopeStructs[st.ID] = true
		}
	}
	return p
}

// closeOverEmbedders adds to set every interface embedding one of it,
// transitively.
func (p *pairing) closeOverEmbedders(set map[int64]bool) {
	stack := keys(set)
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, up := range p.embeddedBy[id] {
			if !set[up] {
				set[up] = true
				stack = append(stack, up)
			}
		}
	}
}

// embeddedModules returns the modules of the interfaces of set and of
// the interfaces they embed, transitively.
func (p *pairing) embeddedModules(set map[int64]bool) map[int64]bool {
	mods, seen := map[int64]bool{}, map[int64]bool{}
	stack := keys(set)
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[id] || id == 0 {
			continue
		}
		seen[id] = true
		mods[p.byID[id].ModuleID] = true
		stack = append(stack, p.embeds[id]...)
	}
	return mods
}

// loadMethods reads the method sets the pairs need: everything for a
// full run; otherwise the full sets of the scope interfaces (with what
// they embed) and of the scope structs, the methods named like those
// interfaces want or those structs have, and the full sets of the other
// interfaces sharing a method name with a scope struct (the candidates).
func (p *pairing) loadMethods(ms *methodSets, all bool) error {
	if all {
		return ms.loadAll()
	}
	if err := ms.loadModules(p.embeddedModules(p.scopeIfaces)); err != nil {
		return err
	}
	structMods := map[int64]bool{}
	for _, st := range p.structs {
		if p.scopeStructs[st.ID] {
			structMods[st.ModuleID] = true
		}
	}
	if err := ms.loadModules(structMods); err != nil {
		return err
	}
	if err := ms.loadNames(p.names(ms)); err != nil {
		return err
	}
	for _, in := range p.ifaces {
		if !p.scopeIfaces[in.ID] && len(ms.sets[typeKey(in)]) > 0 {
			p.candidates[in.ID] = true
		}
	}
	p.closeOverEmbedders(p.candidates)
	return ms.loadModules(p.embeddedModules(p.candidates))
}

// names returns the method names the scope interfaces want and the scope
// structs have.
func (p *pairing) names(ms *methodSets) map[string]bool {
	out := map[string]bool{}
	for id := range p.scopeIfaces {
		for n := range p.wanted(p.byID[id], ms) {
			out[n] = true
		}
	}
	for _, st := range p.structs {
		if p.scopeStructs[st.ID] {
			for n := range ms.sets[typeKey(st)] {
				out[n] = true
			}
		}
	}
	p.want = map[int64]map[string]bool{}
	return out
}

// wanted returns the method set of an interface: its own methods and
// those of the interfaces it embeds; nil when an embedded type is not a
// workspace interface.
func (p *pairing) wanted(in store.Target, ms *methodSets) map[string]bool {
	if w, ok := p.want[in.ID]; ok {
		return w
	}
	p.want[in.ID] = nil // cycle guard
	w := map[string]bool{}
	for n := range ms.sets[typeKey(in)] {
		w[n] = true
	}
	for _, e := range p.embeds[in.ID] {
		sub, ok := p.byID[e]
		if !ok {
			return nil
		}
		for n := range p.wanted(sub, ms) {
			w[n] = true
		}
		if p.want[e] == nil {
			return nil
		}
	}
	p.want[in.ID] = w
	return w
}

// each calls fn for every pair to check: all pairs in a full run, else
// the scope interfaces against every struct and the candidate interfaces
// against the scope structs.
func (p *pairing) each(fn func(in, st store.Target) error) error {
	var scoped []store.Target
	for _, st := range p.structs {
		if p.scopeStructs[st.ID] {
			scoped = append(scoped, st)
		}
	}
	for _, in := range p.ifaces {
		structs := p.structs
		if p.partial && !p.scopeIfaces[in.ID] {
			if !p.candidates[in.ID] {
				continue
			}
			structs = scoped
		}
		for _, st := range structs {
			if err := fn(in, st); err != nil {
				return err
			}
		}
	}
	return nil
}

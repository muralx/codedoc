package resolve

import (
	"strings"

	"github.com/muralx/codedoc/internal/store"
)

// implements detects Go interface satisfaction (Go has no implements
// clause to resolve): a struct whose method names cover the method set of
// an interface with at least one method gets an implements edge to it,
// when the struct is in the interface's repo or in a repo importing the
// interface's module. An interface's method set includes the methods of
// the interfaces it embeds, transitively; an interface embedding a type
// that did not resolve to a workspace interface (io.Reader, a type set
// term) has an unknown method set and gets no edges. Confidence is local
// within one package, else unique; the edge sits on the struct
// declaration.
//
// With moduleIDs nil every pair is computed (the edges were all deleted).
// Otherwise the edges touching those modules are deleted and only the
// pairs in scope recomputed: the interfaces of the modules (and those
// embedding them) against every struct, and every interface against the
// structs of the modules. Method sets are read only for the modules and
// names those pairs need. A pair a SCIP merge linked with a precise edge
// that is still stored is left alone.
func (r *refResolver) implements(moduleIDs []int64) error {
	if moduleIDs != nil {
		if err := r.b.DeleteDetectedEdges(EdgeImplements, moduleIDs); err != nil {
			return err
		}
	}
	precise, err := r.b.PreciseImplements()
	if err != nil {
		return err
	}
	ifaces, err := r.b.TargetsOfKinds("go", []string{"interface"}, nil)
	if err != nil || len(ifaces) == 0 {
		return err
	}
	structs, err := r.b.TargetsOfKinds("go", []string{"struct"}, nil)
	if err != nil || len(structs) == 0 {
		return err
	}
	embeds, err := r.embeds()
	if err != nil {
		return err
	}
	p := newPairing(ifaces, structs, embeds, moduleIDs)
	ms := &methodSets{b: r.b, sets: map[string]map[string]bool{}, full: map[int64]bool{}}
	if err := p.loadMethods(ms, moduleIDs == nil); err != nil {
		return err
	}
	importers, err := r.importerRepos()
	if err != nil {
		return err
	}
	return p.each(func(in, st store.Target) error {
		if precise[[2]int64{st.ID, in.ID}] {
			return nil
		}
		return r.implementEdge(in, st, p.wanted(in, ms), ms, importers)
	})
}

// embeds maps an interface id to the interfaces it embeds, with 0 for an
// embedded type that is not a workspace interface.
func (r *refResolver) embeds() (map[int64][]int64, error) {
	rows, err := r.b.Embeds("go")
	out := map[int64][]int64{}
	for _, e := range rows {
		out[e.Src] = append(out[e.Src], e.Dst)
	}
	return out, err
}

// implementEdge stores st implements in when st's methods cover want and
// in's module is visible from st's repo.
func (r *refResolver) implementEdge(in, st store.Target, want map[string]bool, ms *methodSets,
	importers map[int64]map[int64]bool) error {
	have := ms.sets[typeKey(st)]
	if len(want) == 0 || len(have) < len(want) {
		return nil
	}
	for m := range want {
		if !have[m] {
			return nil
		}
	}
	if st.RepoID != in.RepoID && !importers[in.ModuleID][st.RepoID] {
		return nil
	}
	conf := ConfidenceUnique
	if st.ModuleID == in.ModuleID {
		conf = ConfidenceLocal
	}
	return r.b.InsertEdge(store.Edge{Src: st.ID, Dst: in.ID, Kind: EdgeImplements, RefKind: "implement",
		Confidence: conf, FileID: st.FileID, Line: st.StartLine})
}

// importerRepos maps a module to the repos holding a module that imports it.
func (r *refResolver) importerRepos() (map[int64]map[int64]bool, error) {
	edges, err := r.b.ModuleEdges()
	if err != nil {
		return nil, err
	}
	out := map[int64]map[int64]bool{}
	for _, e := range edges {
		if out[e.Dst] == nil {
			out[e.Dst] = map[int64]bool{}
		}
		out[e.Dst][r.mods[e.Src].RepoID] = true
	}
	return out, nil
}

// typeKey identifies a type by package and name.
func typeKey(t store.Target) string { return key(t.ModuleID, t.Qualified) }

// methodSets holds method names by receiver type (typeKey). A module in
// full has all its methods loaded; other sets may hold only the methods
// with the names loaded by name.
type methodSets struct {
	b    *store.Batch
	sets map[string]map[string]bool
	full map[int64]bool
}

// add records methods: a method Recv.Name belongs to the type Recv of
// its package.
func (ms *methodSets) add(methods []store.Target) {
	for _, m := range methods {
		recv, _, ok := strings.Cut(m.Qualified, ".")
		if !ok {
			continue
		}
		k := key(m.ModuleID, recv)
		if ms.sets[k] == nil {
			ms.sets[k] = map[string]bool{}
		}
		ms.sets[k][m.Name] = true
	}
}

// loadAll reads every Go method.
func (ms *methodSets) loadAll() error {
	methods, err := ms.b.TargetsOfKinds("go", []string{"method"}, nil)
	ms.add(methods)
	for _, m := range methods {
		ms.full[m.ModuleID] = true
	}
	return err
}

// loadModules reads every method of the modules not read in full yet.
func (ms *methodSets) loadModules(ids map[int64]bool) error {
	var need []int64
	for id := range ids {
		if !ms.full[id] {
			need = append(need, id)
			ms.full[id] = true
		}
	}
	if len(need) == 0 {
		return nil
	}
	methods, err := ms.b.TargetsOfKinds("go", []string{"method"}, need)
	ms.add(methods)
	return err
}

// loadNames reads the methods with the given names in every module.
func (ms *methodSets) loadNames(names map[string]bool) error {
	if len(names) == 0 {
		return nil
	}
	methods, err := ms.b.TargetsOfKindsNamed("go", []string{"method"}, keys(names))
	ms.add(methods)
	return err
}

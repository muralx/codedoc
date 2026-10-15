package resolve

import (
	"errors"
	"sort"

	"github.com/muralx/codedoc/internal/store"
)

// Symbol edge kinds (spec section 8).
const (
	EdgeCalls      = "calls"
	EdgeReferences = "references"
	EdgeImplements = "implements"
	EdgeExtends    = "extends"
)

// Confidence tiers of symbol edges (spec 7.3); ConfidenceImport is shared
// with module edges.
const (
	ConfidenceLocal     = "local"
	ConfidenceUnique    = "unique"
	ConfidenceAmbiguous = "ambiguous"
	// ConfidencePrecise marks edges taken from a SCIP index; heuristic
	// resolution never replaces them (see store.ConfidencePrecise).
	ConfidencePrecise = store.ConfidencePrecise
)

// MaxCandidates caps the candidates of an ambiguous edge; a reference
// with more stays unresolved and records the count.
const MaxCandidates = 20

// edgeKinds maps an extractor reference kind to the edge kind it yields;
// config references are matched against config keys, not symbols.
var edgeKinds = map[string]string{
	"call": EdgeCalls, "type": EdgeReferences, "instantiate": EdgeReferences, "field": EdgeReferences,
	"extend": EdgeExtends, "implement": EdgeImplements,
}

// EdgeKind returns the edge kind a reference kind yields, "" for kinds
// that yield no symbol edge (config references).
func EdgeKind(refKind string) string { return edgeKinds[refKind] }

// RefScope limits a reference resolution run to what one sync touched.
type RefScope struct {
	// Files are the files whose references are all resolved again.
	Files []int64
	// Names are symbol and config key names added or removed, or whose
	// symbols changed kind or arity: references with one of these names,
	// in any file, are resolved again.
	Names []string
	// Fields are the names of Go, TS and Java fields whose declared type
	// may have changed, and of those added or removed: references
	// through a field of that name are resolved again.
	Fields []string
	// Modules are the modules whose Go implements edges are recomputed.
	Modules []int64
}

// ReferencesThen resolves stored references to symbol edges, and config
// candidates to reads of config keys, in one transaction (spec 7.3). With
// a nil scope every edge of the workspace is rebuilt; otherwise only the
// references the scope names are, after their old edges are deleted, plus
// the Go implements edges touching the scope's modules. Run it after
// Imports: the import tier reads resolved imports. then (when not nil)
// runs in the same transaction, so a caller can record that the
// resolution finished atomically with it. References into the
// third-party packages the workspace uses become edges with a dependency
// key (deps spec section 6), read from the shared deps.db the last
// dependency sync of the workspace recorded (store.DepsDBKey).
func ReferencesThen(s *store.Store, scope *RefScope, then func(*store.Batch) error) error {
	return s.Batch(func(b *store.Batch) (err error) {
		r, err := newRefResolver(b)
		if err != nil {
			return err
		}
		r.deps = newDepSource(b)
		defer func() { err = errors.Join(err, r.deps.close()) }()
		if scope == nil {
			err = r.all()
		} else {
			err = r.scoped(*scope)
		}
		if err != nil || then == nil {
			return err
		}
		return then(b)
	})
}

// refResolver resolves references through one batch against symbols,
// imports and modules loaded into memory.
type refResolver struct {
	b        *store.Batch
	mods     map[int64]store.Module
	children map[int64][]int64
	subtrees map[int64]map[int64]bool
	packages map[int64]map[int64]bool
	idx      *targetIndex
	uniques  map[uniqueKey]outcome
	imports  map[int64][]store.Import
	// importsAll is true when imports holds every file's imports;
	// importsRead marks the files whose imports were read one by one.
	importsAll  bool
	importsRead map[int64]bool
	memberCache map[string][]store.Target
	// deps resolves into third-party packages.
	deps *depSource
}

func newRefResolver(b *store.Batch) (*refResolver, error) {
	mods, err := b.Modules()
	if err != nil {
		return nil, err
	}
	r := &refResolver{b: b, mods: make(map[int64]store.Module, len(mods)), children: map[int64][]int64{},
		subtrees: map[int64]map[int64]bool{}, packages: map[int64]map[int64]bool{},
		importsRead: map[int64]bool{}, memberCache: map[string][]store.Target{}}
	for _, m := range mods {
		r.mods[m.ID] = m
		if m.ParentID != 0 {
			r.children[m.ParentID] = append(r.children[m.ParentID], m.ID)
		}
	}
	return r, nil
}

// all rebuilds every heuristic edge of the workspace; precise edges and
// the references they come from are kept.
func (r *refResolver) all() error {
	if err := r.b.DeleteHeuristicEdges(); err != nil {
		return err
	}
	refs, err := r.b.References(nil, nil)
	if err != nil {
		return err
	}
	if refs, err = r.dropPrecise(refs, nil); err != nil {
		return err
	}
	refs, configs := splitConfig(refs)
	if err := r.load(nil, nil); err != nil {
		return err
	}
	if err := r.configReads(configs, true); err != nil {
		return err
	}
	if err := r.resolveEach(refs); err != nil {
		return err
	}
	return r.implements(nil)
}

// scoped resolves the references of sc.Files and those named in
// sc.Names again (config reads through a constant whose value is one of
// sc.Names too), then the implements edges of sc.Modules.
func (r *refResolver) scoped(sc RefScope) error {
	refs, err := r.b.References(nonNil(sc.Files), nonNil(sc.Names))
	if err != nil {
		return err
	}
	through, err := r.b.ReferencesThrough(sc.Fields)
	if err != nil {
		return err
	}
	keyed, err := r.b.ReferencesKeyed(nonNil(sc.Names))
	if err != nil {
		return err
	}
	refs = mergeRefs(mergeRefs(refs, through), keyed)
	if refs, err = r.dropPrecise(refs, refIDs(refs)); err != nil {
		return err
	}
	ids := make([]int64, len(refs))
	names, files := map[string]bool{}, map[int64]bool{}
	for i, ref := range refs {
		ids[i], names[ref.Name], files[ref.FileID] = ref.ID, true, true
	}
	if err := r.b.DeleteReferenceEdges(ids); err != nil {
		return err
	}
	refs, configs := splitConfig(refs)
	if err := r.load(keys(names), keys(files)); err != nil {
		return err
	}
	if err := r.configReads(configs, false); err != nil {
		return err
	}
	if err := r.resolveEach(refs); err != nil {
		return err
	}
	if len(sc.Modules) == 0 {
		return nil
	}
	return r.implements(sc.Modules)
}

// load reads the candidate symbols named in names and the imports of
// files; nil reads all of them.
func (r *refResolver) load(names []string, files []int64) error {
	targets, err := r.b.Targets(names)
	if err != nil {
		return err
	}
	r.idx, r.uniques = newTargetIndex(targets), map[uniqueKey]outcome{}
	imps, err := r.b.Imports(files)
	if err != nil {
		return err
	}
	r.imports, r.importsAll = map[int64][]store.Import{}, files == nil
	for _, imp := range imps {
		r.imports[imp.FileID] = append(r.imports[imp.FileID], imp)
	}
	return nil
}

// resolveEach resolves refs and stores an edge per resolved reference.
// Supertype references go first: Java inherited members resolve through
// the extends edges they yield.
func (r *refResolver) resolveEach(refs []store.Reference) error {
	sort.SliceStable(refs, func(i, j int) bool { return supertypeRef(refs[i]) && !supertypeRef(refs[j]) })
	for _, ref := range refs {
		out := r.resolve(ref)
		if out.confidence == "" {
			if out.count != ref.Candidates {
				if err := r.b.SetReferenceCandidates(ref.ID, out.count); err != nil {
					return err
				}
			}
			continue
		}
		if ref.Candidates != 0 {
			if err := r.b.SetReferenceCandidates(ref.ID, 0); err != nil {
				return err
			}
		}
		e := store.Edge{Src: ref.SymbolID, Kind: edgeKinds[ref.Kind], RefKind: ref.Kind, Confidence: out.confidence,
			FileID: ref.FileID, Line: ref.Line, ReferenceID: ref.ID}
		switch {
		case out.depKey != "":
			e.DepKey = out.depKey
		case out.confidence == ConfidenceAmbiguous:
			e.Candidates = out.dst
		default:
			e.Dst = out.dst[0]
		}
		if err := r.b.InsertEdge(e); err != nil {
			return err
		}
	}
	return nil
}

// dropPrecise leaves out the references that have a precise edge: a SCIP
// merge resolved them for certain, and they are resolved heuristically
// again only once their row is rewritten (which deletes the edge). ids
// are the reference ids to check, nil for the whole workspace.
func (r *refResolver) dropPrecise(refs []store.Reference, ids []int64) ([]store.Reference, error) {
	precise, err := r.b.PreciseReferences(ids)
	if err != nil || len(precise) == 0 {
		return refs, err
	}
	out := refs[:0]
	for _, ref := range refs {
		if !precise[ref.ID] {
			out = append(out, ref)
		}
	}
	return out, nil
}

func refIDs(refs []store.Reference) []int64 {
	ids := make([]int64, len(refs))
	for i, ref := range refs {
		ids[i] = ref.ID
	}
	return ids
}

// mergeRefs appends the references of more not in refs.
func mergeRefs(refs, more []store.Reference) []store.Reference {
	seen := make(map[int64]bool, len(refs))
	for _, r := range refs {
		seen[r.ID] = true
	}
	for _, r := range more {
		if !seen[r.ID] {
			seen[r.ID] = true
			refs = append(refs, r)
		}
	}
	return refs
}

func supertypeRef(ref store.Reference) bool { return ref.Kind == "extend" || ref.Kind == "implement" }

// subtree returns a module and its descendants by parent link.
func (r *refResolver) subtree(id int64) map[int64]bool {
	if set, ok := r.subtrees[id]; ok {
		return set
	}
	set := map[int64]bool{}
	stack := []int64{id}
	for len(stack) > 0 {
		m := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if set[m] {
			continue
		}
		set[m] = true
		stack = append(stack, r.children[m]...)
	}
	r.subtrees[id] = set
	return set
}

// nonNil turns nil into an empty slice, which scoped store reads treat as
// "nothing" rather than "everything".
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func keys[K comparable](m map[K]bool) []K {
	out := make([]K, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

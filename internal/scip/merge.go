package scip

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/muralx/codedoc/internal/gitmeta"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/parse"
	"github.com/muralx/codedoc/internal/resolve"
	"github.com/muralx/codedoc/internal/store"
)

// Stats counts what one Merge stored.
type Stats struct {
	// Files counts the repo files whose occurrences were merged; Skipped
	// counts those left to heuristic edges because they changed since the
	// SCIP commit.
	Files, Skipped int
	// Mapped counts the symbols mapped to a SCIP symbol.
	Mapped int
	// Precise counts the reference edges stored as precise; Added is the
	// subset whose reference row the merge added, for occurrences the
	// extractor did not record.
	Precise, Added int
	// Implements counts the precise implements edges.
	Implements int
	// Dropped counts the precise edges of trusted files an earlier merge
	// stored that this one did not confirm (see revalidate).
	Dropped int
}

// Merge stores the precise facts of a SCIP index of repo built at commit
// scipCommit (spec 7.4), under the store's write lock and in one
// transaction:
//
//   - Files changed since scipCommit (committed, uncommitted or
//     untracked) are left out and keep their heuristic edges. With an
//     empty scipCommit (no git) every file is used.
//   - A definition maps its SCIP symbol to our symbol in that file whose
//     span contains the occurrence line and whose name is the identifier
//     at the occurrence; the mapping replaces the repo's scip_symbol rows
//     of the languages the index covers.
//   - A reference to a SCIP symbol mapped in any repo of the workspace
//     replaces the edges of our reference row at that line with that name
//     by one precise edge, or adds a reference row when the extractor
//     recorded none there. References to unmapped (external) symbols keep
//     their heuristic edges.
//   - A type's implementation relationships become precise implements
//     edges.
//   - The precise edges of trusted files this merge does not store again
//     are dropped, with the reference rows an earlier merge added for
//     them; the references and Go implements edges they leave are
//     queued and resolved by the heuristic tiers once the lock is
//     released (an interrupted run leaves them to the next Ensure).
//
// Last, repo.scip_commit is set to scipCommit.
func Merge(s *store.Store, repo store.Repo, ix *Index, scipCommit string) (Stats, error) {
	st, queued, err := mergeLocked(s, repo, ix, scipCommit)
	if err != nil || !queued {
		return st, err
	}
	return st, index.ResolvePending(s, index.Options{})
}

// mergeLocked is Merge under the write lock; queued reports whether it
// left references to resolve.
func mergeLocked(s *store.Store, repo store.Repo, ix *Index, scipCommit string) (st Stats, queued bool, err error) {
	unlock, err := s.WriteLock()
	if err != nil {
		return Stats{}, false, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	m, err := newMerger(s, repo, ix, scipCommit)
	if err != nil {
		return Stats{}, false, err
	}
	if err := m.plan(); err != nil {
		return Stats{}, false, err
	}
	if err := s.Batch(m.write); err != nil {
		return Stats{}, false, err
	}
	return m.stats, len(m.drop.files)+len(m.drop.modules) > 0, nil
}

// MapDefinitions stores the scip_symbol rows a Merge of the same index
// would store (the mappings of its definitions in trusted files),
// replacing the repo's rows of the languages the index covers, so the
// merges of other repos' indexes find them before this one is merged.
func MapDefinitions(s *store.Store, repo store.Repo, ix *Index, scipCommit string) (err error) {
	unlock, err := s.WriteLock()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	m, err := newMerger(s, repo, ix, scipCommit)
	if err != nil {
		return err
	}
	m.mapDefinitions()
	return s.Batch(func(b *store.Batch) error { return b.ReplaceSCIPSymbols(m.prefixes(), m.mapped) })
}

// merger plans one Merge from reads, then writes it in one batch.
type merger struct {
	s      *store.Store
	repo   store.Repo
	ix     *Index
	commit string
	// paths lists the trusted files with occurrences in path order; docs
	// holds their occurrences in position order, files their rows, syms
	// their symbols by file id and lines their text.
	paths []string
	docs  map[string][]Occurrence
	files map[string]store.File
	syms  map[int64][]store.Symbol
	lines map[string][]string
	// fams are the language families of the files the index covers.
	fams map[string]bool
	// defs maps the SCIP symbols defined in trusted files to our symbols;
	// targets maps every known SCIP symbol (symbolKey) to its symbol.
	defs    map[string]store.Symbol
	mapped  []store.SCIPSymbol
	targets map[string]store.SCIPSymbol
	edges   []store.Edge
	added   []addedRef
	impls   []implEdge
	drop    revalidation
	stats   Stats
}

// addedRef is a reference row the merge adds with its precise edge.
type addedRef struct {
	ref store.Reference
	dst int64
}

// implEdge is a precise implements edge on the implementing type.
type implEdge struct {
	src, dst, file int64
	line           int
}

func newMerger(s *store.Store, repo store.Repo, ix *Index, commit string) (*merger, error) {
	m := &merger{s: s, repo: repo, ix: ix, commit: commit, docs: map[string][]Occurrence{},
		files: map[string]store.File{}, syms: map[int64][]store.Symbol{}, lines: map[string][]string{},
		fams: map[string]bool{}, defs: map[string]store.Symbol{}, targets: map[string]store.SCIPSymbol{}}
	rows, err := s.FilesByRepo(repo.ID)
	if err != nil {
		return nil, err
	}
	changed, err := changedFiles(repo.Path, commit)
	if err != nil {
		return nil, err
	}
	m.group(rows, changed)
	kept := m.paths[:0]
	for _, p := range m.paths {
		ok, err := m.load(p)
		if err != nil {
			return nil, err
		}
		if ok {
			kept = append(kept, p)
		}
	}
	m.paths, m.stats.Files = kept, len(kept)
	return m, nil
}

// changedFiles returns the paths that changed since commit, committed or
// not, plus untracked files; nothing without a commit.
func changedFiles(root, commit string) (map[string]bool, error) {
	out := map[string]bool{}
	if commit == "" {
		return out, nil
	}
	since, err := gitmeta.ChangedSince(root, commit)
	if err != nil {
		return nil, err
	}
	dirty, err := gitmeta.DirtyFiles(root)
	if err != nil {
		return nil, err
	}
	for _, p := range append(since, dirty...) {
		out[p] = true
	}
	return out, nil
}

// group sorts the occurrences of indexed files into trusted documents
// and records the language families the index covers.
func (m *merger) group(rows []store.File, changed map[string]bool) {
	byPath := make(map[string]store.File, len(rows))
	for _, f := range rows {
		byPath[f.Path] = f
	}
	skipped := map[string]bool{}
	for _, o := range m.ix.Occurrences {
		f, ok := byPath[o.Path]
		if !ok {
			continue
		}
		m.fams[parse.Family(f.Lang)] = true
		if changed[o.Path] {
			skipped[o.Path] = true
			continue
		}
		if _, ok := m.files[o.Path]; !ok {
			m.files[o.Path] = f
			m.paths = append(m.paths, o.Path)
		}
		m.docs[o.Path] = append(m.docs[o.Path], o)
	}
	sort.Strings(m.paths)
	for _, occ := range m.docs {
		sort.SliceStable(occ, func(i, j int) bool {
			return occ[i].Line < occ[j].Line || occ[i].Line == occ[j].Line && occ[i].Col < occ[j].Col
		})
	}
	m.stats.Files, m.stats.Skipped = len(m.paths), len(skipped)
}

// load reads the symbols and text of a trusted file; a file deleted
// since it was indexed is left out (false).
func (m *merger) load(path string) (bool, error) {
	f := m.files[path]
	src, err := os.ReadFile(filepath.Join(m.repo.Path, filepath.FromSlash(path)))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	m.lines[path] = strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n")
	m.syms[f.ID], err = m.s.SymbolsInFile(f.ID)
	return err == nil, err
}

// plan maps definitions, loads the workspace's mappings and plans the
// precise reference and implements edges.
func (m *merger) plan() error {
	m.mapDefinitions()
	if err := m.loadTargets(); err != nil {
		return err
	}
	for _, p := range m.paths {
		if err := m.references(p); err != nil {
			return err
		}
	}
	if err := m.implementations(); err != nil {
		return err
	}
	if err := m.revalidate(); err != nil {
		return err
	}
	m.stats.Mapped, m.stats.Implements = len(m.mapped), len(m.impls)
	m.stats.Precise, m.stats.Added = len(m.edges)+len(m.added), len(m.added)
	return nil
}

// prefixes returns the stable key prefixes of the repo's symbols in the
// language families the index covers.
func (m *merger) prefixes() []string {
	repo := strings.ReplaceAll(m.repo.Name, ":", "/")
	var out []string
	for fam := range m.fams {
		out = append(out, repo+":"+fam+":")
	}
	sort.Strings(out)
	return out
}

// loadTargets maps every SCIP symbol known to the workspace to its
// symbol: the stored mappings of other repos and languages, then this
// merge's definitions.
func (m *merger) loadTargets() error {
	rows, err := m.s.SCIPSymbols()
	if err != nil {
		return err
	}
	prefixes := m.prefixes()
	for _, r := range rows {
		if !hasAnyPrefix(r.StableKey, prefixes) {
			m.targets[symbolKey(r.SCIP)] = r
		}
	}
	for _, r := range m.mapped {
		m.targets[symbolKey(r.SCIP)] = r
	}
	return nil
}

// write stores the planned mappings and edges and the SCIP commit.
func (m *merger) write(b *store.Batch) error {
	if err := b.ReplaceSCIPSymbols(m.prefixes(), m.mapped); err != nil {
		return err
	}
	if err := m.dropUnconfirmed(b); err != nil {
		return err
	}
	for _, e := range m.edges {
		if err := b.ReplaceReferenceEdges(e); err != nil {
			return err
		}
	}
	for _, a := range m.added {
		id, err := b.InsertReference(a.ref)
		if err != nil {
			return err
		}
		if err := b.InsertEdge(preciseEdge(a.ref, id, a.dst)); err != nil {
			return err
		}
	}
	for _, e := range m.impls {
		if err := b.SetPreciseImplements(e.src, e.dst, e.file, e.line); err != nil {
			return err
		}
	}
	return b.SetSCIPCommit(m.repo.ID, m.commit)
}

// preciseEdge is the precise edge of reference ref (stored as id) to dst.
func preciseEdge(ref store.Reference, id, dst int64) store.Edge {
	return store.Edge{Src: ref.SymbolID, Dst: dst, Kind: resolve.EdgeKind(ref.Kind), RefKind: ref.Kind,
		Confidence: resolve.ConfidencePrecise, FileID: ref.FileID, Line: ref.Line, ReferenceID: id}
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

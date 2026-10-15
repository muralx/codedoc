package index

import (
	"errors"
	"sort"

	"github.com/muralx/codedoc/internal/embed"
	"github.com/muralx/codedoc/internal/store"
)

// embedBatch is the least number of texts embedded and written
// together: symbols are taken whole until that many texts are reached,
// then embedded and written in one transaction.
const embedBatch = 32

// EnsureEmbedMax is the most stale symbols and modules a freshness check
// or a search embeds; above it, or above ensurePassages times as many
// texts, they are left for codedoc index.
const EnsureEmbedMax = 64

// ensurePassages bounds the texts of a limited pass per stale row: long
// bodies have many passages, and a search must not wait for them.
const ensurePassages = 2

// VecResult summarises an embedding pass.
type VecResult struct {
	// Symbols and Modules count the symbols and modules embedded;
	// Passages the vectors written for those symbols, one per passage.
	Symbols, Modules, Passages int
	// Removed counts the symbols and modules whose vectors were deleted
	// because they are not embedded any more.
	Removed int
	// Deferred counts the stale symbols and modules left alone because
	// there were more than the pass's limit.
	Deferred int
	// Unavailable is true when the embedder could not load its model.
	Unavailable bool
}

// vecItem is a symbol or module to embed: the texts of its passages and
// where their vectors go.
type vecItem struct {
	id       int64
	module   bool
	hash     string
	passages []vecPassage
}

// vecPassage is one text to embed with the lines it covers (see
// store.PassageVec).
type vecPassage struct {
	fromDoc    bool
	start, end int
	text       string
}

// longest is the size of the item's longest text.
func (it vecItem) longest() int {
	n := 0
	for _, p := range it.passages {
		n = max(n, len(p.text))
	}
	return n
}

// EmbedVectors brings the vectors of a workspace scope up to date: every
// symbol and documented module without a vector, or whose text hash or
// vector size changed, is embedded (as a document, see embed.Document),
// one vector per passage of its text (see symbolItems), shortest texts
// first, in batches of at least embedBatch texts, each written in its own
// transaction after its vectors are computed. Undocumented symbols are
// embedded from their source whatever sc.Bodies says. The vectors of the
// scope's repo that are not embedded any more are deleted first. With
// limit above 0 and more stale rows than limit (or more than
// ensurePassages texts per allowed row), nothing is embedded and the rows
// are counted as deferred. A symbol whose file changed on disk since it
// was indexed is skipped and stays stale. An embedder that cannot load
// its model ends the pass without an error (Unavailable).
func EmbedVectors(s *store.Store, e embed.Embedder, sc store.VecScope, limit int) (VecResult, error) {
	var res VecResult
	var err error
	sc.Bodies = true
	if res.Removed, err = s.DeleteUnembeddedVecs(sc.RepoID); err != nil {
		return res, err
	}
	dims := e.Dimensions()
	syms, err := s.StaleSymbolVecs(sc, dims)
	if err != nil {
		return res, err
	}
	mods, err := staleModules(s, sc, dims)
	if err != nil {
		return res, err
	}
	if n := len(syms) + len(mods); limit > 0 && n > limit {
		res.Deferred = n
		return res, nil
	}
	items, err := symbolItems(s, syms)
	if err != nil {
		return res, err
	}
	return res, embedItems(s, e, append(items, mods...), limit, &res)
}

// EmbedAPIVectors brings the vectors of package versions of a dependency
// database up to date (deps spec section 8): every documented symbol and
// module of the repo rows repoIDs without a current vector is embedded
// from its signature and doc alone, a long doc one vector per passage,
// since deps.db stores no bodies and the package directory may be gone.
// A package version is immutable, so its vectors are computed once. limit
// and an unavailable model behave as in EmbedVectors; nothing takes the
// deps.db write lock.
func EmbedAPIVectors(s *store.Store, e embed.Embedder, repoIDs []int64, limit int) (VecResult, error) {
	var res VecResult
	dims := e.Dimensions()
	var items []vecItem
	for _, id := range repoIDs {
		sc := store.VecScope{RepoID: id}
		syms, err := s.StaleSymbolVecs(sc, dims)
		if err != nil {
			return res, err
		}
		mods, err := staleModules(s, sc, dims)
		if err != nil {
			return res, err
		}
		for _, sy := range syms {
			items = append(items, vecItem{id: sy.ID, hash: sy.VectorHash, passages: docPassages(sy, nil, "")})
		}
		items = append(items, mods...)
	}
	if limit > 0 && len(items) > limit {
		res.Deferred = len(items)
		return res, nil
	}
	return res, embedItems(s, e, items, limit, &res)
}

// embedItems embeds items, those with the shortest texts first, in
// batches of whole items holding at least embedBatch texts, each batch
// written in its own transaction, and counts them in res. With limit
// above 0 and more than ensurePassages texts per allowed item, nothing
// is embedded and the items are counted as deferred. A model that cannot
// load ends it without an error.
func embedItems(s *store.Store, e embed.Embedder, items []vecItem, limit int, res *VecResult) error {
	texts := 0
	for _, it := range items {
		texts += len(it.passages)
	}
	if limit > 0 && texts > ensurePassages*limit {
		res.Deferred = len(items)
		return nil
	}
	// Batches of texts of similar length pad little (see
	// embed.ONNXEmbedder.EmbedBatch).
	sort.SliceStable(items, func(i, j int) bool { return items[i].longest() < items[j].longest() })
	for lo := 0; lo < len(items); {
		hi, n := lo, 0
		for ; hi < len(items) && n < embedBatch; hi++ {
			n += len(items[hi].passages)
		}
		if err := writeVectors(s, e, items[lo:hi], res); err != nil {
			if errors.Is(err, embed.ErrUnavailable) {
				res.Unavailable = true
				return nil
			}
			return err
		}
		lo = hi
	}
	return nil
}

// writeVectors embeds the texts of one batch of items, shortest first,
// and then stores their vectors in one transaction.
func writeVectors(s *store.Store, e embed.Embedder, chunk []vecItem, res *VecResult) error {
	type slot struct{ item, passage int }
	var slots []slot
	for i, it := range chunk {
		for j := range it.passages {
			slots = append(slots, slot{i, j})
		}
	}
	text := func(sl slot) string { return chunk[sl.item].passages[sl.passage].text }
	sort.SliceStable(slots, func(i, j int) bool { return len(text(slots[i])) < len(text(slots[j])) })
	texts := make([]string, len(slots))
	for i, sl := range slots {
		texts[i] = text(sl)
	}
	vecs, err := embed.Documents(e, texts)
	if err != nil {
		return err
	}
	byItem := make([][]store.PassageVec, len(chunk))
	for i, it := range chunk {
		byItem[i] = make([]store.PassageVec, len(it.passages))
	}
	for i, sl := range slots {
		p := chunk[sl.item].passages[sl.passage]
		byItem[sl.item][sl.passage] = store.PassageVec{FromDoc: p.fromDoc, Start: p.start, End: p.end, Vec: vecs[i]}
	}
	return s.Batch(func(b *store.Batch) error { return storeVectors(b, chunk, byItem, res) })
}

// storeVectors writes the vectors of a batch of items and counts them.
func storeVectors(b *store.Batch, chunk []vecItem, vecs [][]store.PassageVec, res *VecResult) error {
	for i, it := range chunk {
		if it.module {
			res.Modules++
			if err := b.UpsertModuleVec(it.id, it.hash, vecs[i][0].Vec); err != nil {
				return err
			}
			continue
		}
		res.Symbols++
		res.Passages += len(vecs[i])
		if err := b.ReplaceSymbolVecs(it.id, it.hash, vecs[i]); err != nil {
			return err
		}
	}
	return nil
}

// staleModules returns the documented modules of a scope whose vector is
// missing, of another size, or computed from another text.
func staleModules(s *store.Store, sc store.VecScope, dims int) ([]vecItem, error) {
	mods, err := s.ModuleVecs(sc)
	if err != nil {
		return nil, err
	}
	var out []vecItem
	for _, m := range mods {
		text := embed.ModuleText(m.Identity, m.Path, m.DocText)
		if h := embed.TextHash(text); h != m.Hash || m.Dims != dims {
			out = append(out, vecItem{id: m.ID, module: true, hash: h, passages: []vecPassage{{text: text}}})
		}
	}
	return out, nil
}

// embedTouched embeds the vectors of the symbols of the files a sync
// extracted and of its repo's modules, when the
// options carry an embedder that needs no model load (see embed.Warm) and
// at most EnsureEmbedMax vectors are stale; otherwise they stay stale
// for codedoc index or a search to catch up.
func embedTouched(s *store.Store, opts Options, sy *syncer) error {
	if opts.Embedder == nil || !embed.Warm(opts.Embedder) {
		return nil
	}
	sc := store.VecScope{RepoID: sy.repoID, Touched: true, FileIDs: sy.extracted}
	_, err := EmbedVectors(s, opts.Embedder, sc, EnsureEmbedMax)
	return err
}

// VectorCounts counts the symbols and documented modules of a workspace
// repo, what an embedding pass embeds, and those whose vectors are up to
// date: owners, not passages, so a symbol with several passage vectors
// counts once. dims above 0 also requires the vector size of the
// configured embedder.
func VectorCounts(s *store.Store, repoID int64, dims int) (embedded, documented int, err error) {
	return vectorCounts(s, store.VecScope{RepoID: repoID, Bodies: true}, dims)
}

// APIVectorCounts is VectorCounts for a package version of a dependency
// database, whose documented symbols and modules alone are embedded,
// with vectors of any size.
func APIVectorCounts(s *store.Store, repoID int64) (embedded, documented int, err error) {
	return vectorCounts(s, store.VecScope{RepoID: repoID}, 0)
}

func vectorCounts(s *store.Store, sc store.VecScope, dims int) (embedded, documented int, err error) {
	embedded, documented, err = s.SymbolVecCounts(sc, dims)
	if err != nil {
		return 0, 0, err
	}
	mods, err := s.ModuleVecs(sc)
	if err != nil {
		return 0, 0, err
	}
	for _, m := range mods {
		documented++
		if m.Hash == embed.TextHash(embed.ModuleText(m.Identity, m.Path, m.DocText)) && (dims == 0 || m.Dims == dims) {
			embedded++
		}
	}
	return embedded, documented, nil
}

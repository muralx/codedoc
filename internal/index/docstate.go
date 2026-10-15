package index

import (
	"github.com/muralx/codedoc/internal/docs"
	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/gitmeta"
	"github.com/muralx/codedoc/internal/store"
)

// docStates sets the doc body hash and doc state of a file's extracted
// symbols from the rows held before (spec 7.5), matched by stable key:
// the file's own rows first, else the rows this sync parked or deleted
// in other files, so a symbol that moved keeps its state. A documented
// symbol without a prior row (or with one that has no body hash, as after
// a migration, or from an older extractor version) or whose doc changed
// is seeded from git blame of the file, read at most once per file and
// only when needed (preExtract may have read it already). An undocumented
// member of a documented Go group is documented by the group: ok.
func (sy *syncer) docStates(b *store.Batch, fileID int64, path string, ex *extracted) error {
	priors, err := b.DocPriors(fileID)
	if err != nil {
		return err
	}
	blame, loaded := ex.blame, ex.blamed
	members := memberIndex(ex.syms)
	for i := range ex.syms {
		s := &ex.syms[i]
		if s.DocHash == "" && ex.grouped[i] {
			s.DocBodyHash, s.DocState = "", string(docs.OK)
			continue
		}
		seed := func() docs.Seed {
			if !loaded {
				blame, loaded = sy.blame(path), true
			}
			if blame == nil {
				return docs.SeedUntracked
			}
			return docs.SeedFromBlame(blame, docs.Lines{Start: s.StartLine, End: s.EndLine},
				docs.Lines{Start: s.DocStartLine, End: s.DocEndLine}, memberSkips(ex.syms, members, *s)...)
		}
		h, st := docs.Next(sy.prior(priors, path, s.StableKey), s.DocHash, s.BodyHash, seed)
		s.DocBodyHash, s.DocState = h, string(st)
	}
	return nil
}

// prior returns the doc tracking state stored for a stable key of the
// file at path, nil when there is none to compare with: the file's own
// rows are ignored when they come from another extractor version.
func (sy *syncer) prior(priors map[string]store.DocPrior, path, key string) *docs.Prior {
	p, ok := priors[key]
	if sy.reseed[path] {
		ok = false
	}
	if !ok {
		p, ok = sy.moved[key]
	}
	if !ok || p.BodyHash == "" {
		return nil
	}
	return &docs.Prior{DocHash: p.DocHash, BodyHash: p.BodyHash, DocBodyHash: p.DocBodyHash}
}

// remember records the doc tracking state of the symbols of files
// before they are parked or deleted, unless they come from another
// extractor version.
func (sy *syncer) remember(b *store.Batch, files ...store.File) error {
	var ids []int64
	for _, f := range files {
		if !sy.reseed[f.Path] {
			ids = append(ids, f.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	priors, err := b.DocPriors(ids...)
	if err != nil {
		return err
	}
	if sy.moved == nil {
		sy.moved = map[string]store.DocPrior{}
	}
	for k, p := range priors {
		sy.moved[k] = p
	}
	return nil
}

// memberIndex maps the qualified name of every container to the
// positions of the symbols naming it as their container.
func memberIndex(syms []store.Symbol) map[string][]int {
	out := map[string][]int{}
	for i, s := range syms {
		if s.Container != "" {
			out[s.Container] = append(out[s.Container], i)
		}
	}
	return out
}

// memberSkips returns, for a container, the lines blame seeding skips:
// the bodies and docs of its direct members (those inside its span), so
// only its own header and its member declarations count (spec 7.5).
// Other symbols skip nothing.
func memberSkips(syms []store.Symbol, members map[string][]int, c store.Symbol) []docs.Lines {
	if !extract.IsContainer(extract.Kind(c.Kind)) {
		return nil
	}
	var out []docs.Lines
	for _, i := range members[c.Qualified] {
		m := syms[i]
		if m.StartLine < c.StartLine || m.EndLine > c.EndLine {
			continue
		}
		if m.EndLine > m.SigEndLine {
			out = append(out, docs.Lines{Start: m.SigEndLine + 1, End: m.EndLine})
		}
		if m.DocStartLine > 0 {
			out = append(out, docs.Lines{Start: m.DocStartLine, End: m.DocEndLine})
		}
	}
	return out
}

// blame returns the commit time of every line of a file, or nil when
// the repo is no git repository, the file is untracked, or git cannot
// blame it: such files seed their docs as current.
func (sy *syncer) blame(path string) map[int]int64 {
	if !sy.isRepo {
		return nil
	}
	m, err := gitmeta.Blame(sy.root, path)
	if err != nil {
		return nil
	}
	return m
}

// needsSeed reports whether an extraction of a file the index does not
// know yet (or whose row was never hashed, as after a migration) holds a
// documented symbol: its doc states will be seeded from blame.
func needsSeed(ex extracted, old store.File, known bool) bool {
	if known && old.Hash != "" {
		return false
	}
	for _, s := range ex.syms {
		if s.DocHash != "" {
			return true
		}
	}
	return false
}

package index

import (
	"path"
	"strings"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/extract/doc"
	"github.com/muralx/codedoc/internal/store"
)

// edgeReferences is the kind of the edge a link between documents makes.
const edgeReferences = "references"

// documentTexts prepares the rows of an extracted document: every
// symbol's doc is ok (a section's text is its own documentation, so
// nothing tracks it), and its own text is cut out of src: from its
// heading to its first sub-section, which the section text index
// covers. It returns the document's title, "" when it has none but its
// file name, and the own texts in the order of syms.
func documentTexts(p string, src []byte, syms []store.Symbol) (title string, texts []string) {
	texts = make([]string, len(syms))
	for i := range syms {
		s := &syms[i]
		s.DocState = "ok"
		end := s.EndByte
		if i+1 < len(syms) && syms[i+1].StartByte < end {
			end = syms[i+1].StartByte
		}
		start := s.StartByte
		if s.Kind == string(extract.KindSection) {
			start = sigEnd(src, s.StartByte, s.SigEndLine-s.StartLine+1)
		}
		if start < end {
			texts[i] = strings.TrimSpace(string(src[start:end]))
		}
	}
	if len(syms) > 0 && syms[0].Signature != path.Base(p) {
		title = syms[0].Signature
	}
	return title, texts
}

// sigEnd is the offset after the first lines lines from start: the end
// of a heading.
func sigEnd(src []byte, start int64, lines int) int64 {
	for ; lines > 0 && start < int64(len(src)); lines-- {
		nl := strings.IndexByte(string(src[start:]), '\n')
		if nl < 0 {
			return int64(len(src))
		}
		start += int64(nl) + 1
	}
	return start
}

// firstSentence returns the first sentence of a doc on one line: the
// title of a code file with a module doc.
func firstSentence(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if i := strings.Index(text, ". "); i >= 0 {
		return text[:i+1]
	}
	return text
}

// storeDocument writes the sections of an extracted document, their
// searchable texts and the document's links. Sections resolve no
// references and take no part in key collisions: a document's keys carry
// its path and its extractor makes them unique.
func (sy *syncer) storeDocument(b *store.Batch, id int64, p string, ex extracted) error {
	sy.docs = true
	res, err := b.SyncFileSymbols(id, 0, ex.syms)
	if err != nil {
		return err
	}
	texts := make(map[int64]string, len(ex.syms))
	byQualified := make(map[string]int64, len(ex.syms))
	for i, s := range ex.syms {
		texts[res.IDs[s.StableKey]] = ex.texts[i]
		byQualified[s.Qualified] = res.IDs[s.StableKey]
	}
	if err := b.ReplaceSectionTexts(id, texts); err != nil {
		return err
	}
	return b.ReplaceDocLinks(id, docLinks(p, ex.refs, byQualified))
}

// docLinks converts the link references of the document at p into link
// rows: the target is resolved against the document's directory (a
// leading slash against the repo root), and links leaving the repo are
// dropped.
func docLinks(p string, refs []extract.Reference, byQualified map[string]int64) []store.DocLink {
	var out []store.DocLink
	for _, r := range refs {
		if r.Kind != extract.RefLink {
			continue
		}
		target := path.Clean(path.Join(path.Dir(p), r.Name))
		if strings.HasPrefix(r.Name, "/") {
			target = path.Clean("." + r.Name)
		}
		if target == "." || target == ".." || strings.HasPrefix(target, "../") {
			continue
		}
		out = append(out, store.DocLink{SymbolID: byQualified[r.Enclosing], Target: target, Anchor: r.Qualifier,
			Line: r.Line})
	}
	return out
}

// linkEdges derives the references edges between the documents of the
// repo from their links again: from the section a link is written in to
// the document it names, or to the section its anchor names. It runs
// when a sync wrote or deleted a document, since any of them may be the
// target of a link elsewhere.
func (sy *syncer) linkEdges(b *store.Batch) error {
	if err := b.DeleteLinkEdges(sy.repoID); err != nil {
		return err
	}
	links, err := b.DocLinks(sy.repoID)
	if err != nil || len(links) == 0 {
		return err
	}
	byFile, err := b.DocSymbols(sy.repoID)
	if err != nil {
		return err
	}
	for _, l := range links {
		dst := linkTarget(byFile[l.TargetFileID], l.Anchor)
		if dst == 0 || dst == l.SymbolID {
			continue
		}
		err := b.InsertEdge(store.Edge{Src: l.SymbolID, Dst: dst, Kind: edgeReferences, RefKind: store.LinkRefKind,
			Confidence: store.LinkConfidence, FileID: l.FileID, Line: l.Line})
		if err != nil {
			return err
		}
	}
	return nil
}

// linkTarget picks, among the symbols of a document, the section whose
// heading slug is anchor, else the document; 0 for a file that is no
// document.
func linkTarget(syms []store.Symbol, anchor string) int64 {
	var document int64
	for _, s := range syms {
		switch {
		case s.Kind == string(extract.KindDocument):
			document = s.ID
		case anchor != "" && doc.Slug(s.Name) == strings.ToLower(anchor):
			return s.ID
		}
	}
	return document
}

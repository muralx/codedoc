package index

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/cespare/xxhash/v2"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/extract/cfg"
	"github.com/muralx/codedoc/internal/parse"
	"github.com/muralx/codedoc/internal/resolve"
	"github.com/muralx/codedoc/internal/stablekey"
	"github.com/muralx/codedoc/internal/store"
)

// extracted holds the symbol rows of one file, with keys not yet checked
// for collisions, its references and imports and its parse error count;
// for a config file, its key definitions and references instead.
type extracted struct {
	syms        []store.Symbol
	refs        []extract.Reference
	imports     []store.Import
	config      *cfg.Result
	parseErrors int
	// moduleDoc is the package or module doc the file carries, if any.
	moduleDoc *extract.Doc
	// blame holds the file's git blame when blamed; nil when it was read
	// and the file has none (see syncer.blame).
	blame  map[int]int64
	blamed bool
	// grouped holds the positions in syms of the symbols documented by
	// the doc of their declaration group (see extract.Symbol.GroupDoc).
	grouped map[int]bool
	// title is the file's title (see store.File.Title).
	title string
	// texts holds, for a document, the own text of each of syms: the
	// text the section text index covers (see documentTexts).
	texts []string
}

// extractFile reads and extracts one file of module mod. Files without a
// registered extractor, and files gone since discovery, yield nothing; a
// file the extractor cannot handle at all counts one parse error. Stable
// keys carry the package identity (spec 5.1), and TypeScript qualified names start
// with the file path relative to the package root (spec 5.1). A document's
// keys carry its path instead of a package (documents spec section 2).
func extractFile(root, repoName string, d Discovered, mod resolve.Module) (extracted, error) {
	ex, ok := extract.For(d.Lang)
	ce := cfg.For(d.Lang)
	if !ok && ce == nil {
		return extracted{}, nil
	}
	if d.Size > maxFileSize {
		return extracted{}, nil // a document too large to section (see inspectLarge)
	}
	src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(d.Path)))
	if errors.Is(err, fs.ErrNotExist) {
		return extracted{}, nil
	}
	if err != nil {
		return extracted{}, err
	}
	if !ok {
		return extractConfig(ce, d, src), nil
	}
	return extractSource(ex, repoName, d, mod, src), nil
}

// extractSource extracts the symbols of one code file or document from
// its content; see extractFile.
func extractSource(ex extract.Extractor, repoName string, d Discovered, mod resolve.Module, src []byte) extracted {
	family := parse.Family(d.Lang)
	extractPath, pkg := d.Path, mod.Package
	switch family {
	case "ts":
		extractPath = mod.RelPath(d.Path)
	case parse.DocFamily:
		pkg = d.Path // a document belongs to no module: its path stands in the key
	}
	res, err := ex.Extract(extractPath, src)
	if err != nil {
		return extracted{parseErrors: 1}
	}
	out := extracted{parseErrors: res.ParseErrors, syms: make([]store.Symbol, len(res.Symbols)), refs: res.References,
		moduleDoc: res.ModuleDoc}
	for i, sy := range res.Symbols {
		out.syms[i] = SymbolRow(stablekey.New(repoName, family, pkg, sy.Qualified, sy.SigHash), sy)
		if sy.GroupDoc {
			if out.grouped == nil {
				out.grouped = map[int]bool{}
			}
			out.grouped[i] = true
		}
	}
	for _, imp := range res.Imports {
		out.imports = append(out.imports, store.Import{Raw: imp.Raw, Alias: imp.Alias, Names: imp.Names, Line: imp.Line})
	}
	if family == parse.DocFamily {
		out.title, out.texts = documentTexts(d.Path, src, out.syms)
	} else if res.ModuleDoc != nil {
		out.title = firstSentence(res.ModuleDoc.Text)
	}
	return out
}

// extractConfig extracts the keys of a config file. A file the extractor
// fails on counts one parse error and keeps the keys found before the
// failure.
func extractConfig(ce cfg.Extractor, d Discovered, src []byte) extracted {
	res, err := ce.Extract(d.Path, src)
	out := extracted{config: res}
	if res == nil {
		out.config = &cfg.Result{Format: d.Lang}
	}
	if err != nil {
		out.parseErrors = 1
	}
	return out
}

// configEntries converts config key definitions and references to store
// entries.
func configEntries(res *cfg.Result) (defs, refs []store.ConfigEntry) {
	for _, d := range res.Defs {
		defs = append(defs, store.ConfigEntry{Key: d.Key, Line: d.Line, Preview: d.Preview})
	}
	for _, r := range res.Refs {
		refs = append(refs, store.ConfigEntry{Key: r.Key, Line: r.Line})
	}
	return defs, refs
}

// SymbolRow converts an extracted symbol into a store row. Its doc state
// is left to docStates, which compares it with the stored row.
func SymbolRow(key stablekey.Key, sy extract.Symbol) store.Symbol {
	row := store.Symbol{
		Kind: string(sy.Kind), Name: sy.Name, Qualified: sy.Qualified, StableKey: key.String(), Container: sy.Container,
		StartLine: sy.Span.StartLine, EndLine: sy.Span.EndLine,
		StartByte: int64(sy.Span.StartByte), EndByte: int64(sy.Span.EndByte),
		SigStartLine: sy.Signature.StartLine, SigEndLine: sy.Signature.EndLine, Signature: sy.SigText,
		Visibility: string(sy.Visibility), IsTest: sy.IsTest, BodyHash: sy.BodyHash, Value: sy.Value,
	}
	if sy.Doc != nil {
		row.DocText = sy.Doc.Text
		row.DocHash = fmt.Sprintf("%016x", xxhash.Sum64String(sy.Doc.Text))
		row.DocStartLine, row.DocEndLine = sy.Doc.Span.StartLine, sy.Doc.Span.EndLine
	}
	return row
}

// referenceRows converts extracted references into store rows, linking
// each to its enclosing symbol through the ids ReplaceFileSymbols
// returned. Overloads share a qualified name (Java): a reference links to
// the one whose span holds its line, else to the first.
func referenceRows(refs []extract.Reference, syms []store.Symbol, ids map[string]int64) []store.Reference {
	byQualified := make(map[string][]store.Symbol, len(syms))
	for _, sy := range syms {
		byQualified[sy.Qualified] = append(byQualified[sy.Qualified], sy)
	}
	out := make([]store.Reference, len(refs))
	for i, r := range refs {
		var id int64
		if cands := byQualified[r.Enclosing]; len(cands) > 0 {
			id = ids[cands[0].StableKey]
			for _, c := range cands {
				if c.StartLine <= r.Line && r.Line <= c.EndLine {
					id = ids[c.StableKey]
					break
				}
			}
		}
		out[i] = store.Reference{SymbolID: id, Kind: string(r.Kind), Name: r.Name,
			Qualifier: r.Qualifier, Line: r.Line, Col: r.Col, Arity: r.Arity,
			RecvType: r.RecvType}
	}
	return out
}

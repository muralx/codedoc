package scip

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"

	pb "github.com/scip-code/scip/bindings/go/scip"
)

// Occurrence is one use of a global SCIP symbol in a document.
type Occurrence struct {
	// Path is the document path relative to the indexed project root.
	Path string
	// Line and EndLine are 1-based; Col and EndCol are the 0-based
	// character offsets the indexer emitted (end exclusive).
	Line, Col, EndLine, EndCol int
	// Symbol is the SCIP symbol string.
	Symbol string
	// Definition is set when the occurrence defines Symbol.
	Definition bool
	// Ref is set when the occurrence references Symbol: neither a
	// definition nor an import.
	Ref bool
}

// SymbolInfo is the metadata of a global SCIP symbol.
type SymbolInfo struct {
	// Symbol is the SCIP symbol string.
	Symbol string
	// DisplayName is the indexer's short name for the symbol, if any.
	DisplayName string
	// Implements lists the symbols this symbol implements.
	Implements []string
}

// Index is the parsed content of a SCIP index. Document-local symbols are
// left out: they cannot be mapped to symbols of other files.
type Index struct {
	// Occurrences lists every global-symbol occurrence in document order.
	Occurrences []Occurrence
	// Symbols holds document and external symbol information by symbol.
	Symbols map[string]SymbolInfo
}

// ParseFile streams the SCIP index at path into an Index.
func ParseFile(path string) (*Index, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("scip: open index: %w", err)
	}
	defer func() { _ = f.Close() }()
	ix := &Index{Symbols: map[string]SymbolInfo{}}
	v := pb.IndexVisitor{
		VisitDocument: func(_ context.Context, d *pb.Document) error {
			return ix.addDocument(d)
		},
		VisitExternalSymbol: func(_ context.Context, si *pb.SymbolInformation) error {
			ix.addSymbol(si)
			return nil
		},
	}
	if err := v.ParseStreaming(context.Background(), f); err != nil {
		return nil, fmt.Errorf("scip: parse %s: %w", path, err)
	}
	return ix, nil
}

func (ix *Index) addDocument(d *pb.Document) error {
	if !insideRoot(d.RelativePath) {
		return nil
	}
	for _, o := range d.Occurrences {
		if o.Symbol == "" || pb.IsLocalSymbol(o.Symbol) {
			continue
		}
		r, ok := o.SourceRange()
		if !ok {
			return fmt.Errorf("document %s: occurrence of %s has no valid range", d.RelativePath, o.Symbol)
		}
		def := o.SymbolRoles&int32(pb.SymbolRole_Definition) != 0
		imp := o.SymbolRoles&int32(pb.SymbolRole_Import) != 0
		ix.Occurrences = append(ix.Occurrences, Occurrence{
			Path:       d.RelativePath,
			Line:       int(r.Start.Line) + 1,
			Col:        int(r.Start.Character),
			EndLine:    int(r.End.Line) + 1,
			EndCol:     int(r.End.Character),
			Symbol:     o.Symbol,
			Definition: def,
			Ref:        !def && !imp,
		})
	}
	for _, si := range d.Symbols {
		ix.addSymbol(si)
	}
	return nil
}

func (ix *Index) addSymbol(si *pb.SymbolInformation) {
	if si.Symbol == "" || pb.IsLocalSymbol(si.Symbol) {
		return
	}
	info := ix.Symbols[si.Symbol]
	info.Symbol = si.Symbol
	if si.DisplayName != "" {
		info.DisplayName = si.DisplayName
	}
	for _, rel := range si.Relationships {
		if rel.IsImplementation && !pb.IsLocalSymbol(rel.Symbol) {
			info.Implements = append(info.Implements, rel.Symbol)
		}
	}
	ix.Symbols[si.Symbol] = info
}

// insideRoot reports whether a document path stays inside the project
// root. scip-go, for one, also indexes generated test mains in the build
// cache under paths such as ../cache/....
func insideRoot(p string) bool {
	c := path.Clean(p)
	return c != "." && c != ".." && !path.IsAbs(c) && !strings.HasPrefix(c, "../")
}

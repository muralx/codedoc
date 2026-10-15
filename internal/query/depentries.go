package query

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/store"
)

// depEntry renders a dependency symbol as one result line, tagged
// [dep name@version]: stable key, path:line relative to the package,
// signature, first sentence of the doc. An inherited member asked
// through a subtype says where it is declared.
func depEntry(d depSym) output.Entry {
	e := output.Entry{Key: d.StableKey, Name: shortName(d.Symbol),
		Loc: spanLoc(d.Path, d.StartLine, d.EndLine), Text: d.Signature,
		Doc: firstSentence(d.DocText), Tags: []string{d.pkg.tag()},
		Extra: map[string]any{"kind": d.Kind, "dep": d.pkg.id.String()}}
	if d.via != "" {
		e.Tags = append(e.Tags, "declared on "+containerName(d.Symbol))
		e.Extra["inherited_by"], e.Extra["declared_on"] = d.via, containerName(d.Symbol)
	}
	return e
}

// depEntries renders dependency symbols in package, path and line order.
func depEntries(syms []depSym) []output.Entry {
	sortDeps(syms)
	out := make([]output.Entry, len(syms))
	for i, d := range syms {
		out[i] = depEntry(d)
	}
	return out
}

// containerName is the name of the type that declares a member: the
// segment before the last dot of its qualified name, "" at top level.
func containerName(sy store.Symbol) string {
	q := sy.Qualified[strings.LastIndex(sy.Qualified, "/")+1:]
	dot := strings.LastIndex(q, ".")
	if dot < 0 {
		return ""
	}
	q = q[:dot]
	return q[strings.LastIndex(q, ".")+1:]
}

// depDetail renders the symbol entry of a dependency symbol: the full
// doc, tags for its kind, package, container and module and for the
// uses the workspace makes of it, and optionally its declaration read
// from the directory the package is installed in. unavailable says why
// a body that was asked for is missing, "" when it is not.
func (c *Ctx) depDetail(d depSym, o SymbolOpts) (e output.Entry, unavailable string, err error) {
	e = depEntry(d)
	root := d.pkg.root()
	e.Loc = spanLoc(d.Path, d.StartLine, d.EndLine)
	if root != "" {
		e.Loc = spanLoc(filepath.Join(root, filepath.FromSlash(d.Path)), d.StartLine, d.EndLine)
	}
	e.Doc = strings.Join(strings.Fields(d.DocText), " ")
	uses, err := c.Store.DepFanIns([]string{d.StableKey})
	if err != nil {
		return e, "", err
	}
	tags := []string{d.Kind, d.pkg.tag()}
	if d.ContainerID != 0 {
		tags = append(tags, "container:"+containerName(d.Symbol))
	}
	tags = append(tags, "module:"+moduleOf(d.Symbol))
	e.Tags = append(append(tags, e.Tags[1:]...), fmt.Sprintf("fan-in:%d", uses[d.StableKey]))
	for k, v := range map[string]any{"module": moduleOf(d.Symbol), "doc": d.DocText, "start_line": d.StartLine,
		"end_line": d.EndLine, "visibility": d.Visibility, "fan_in": uses[d.StableKey], "dir": root} {
		e.Extra[k] = v
	}
	if o.NoBody {
		return e, "", nil
	}
	src, unavailable, err := c.depSource(d, root)
	if err != nil || unavailable != "" {
		return e, unavailable, err
	}
	e.Body, e.BodyStart, e.BodyEnd = body(src, d.Symbol, o.Context)
	withDoc(&e, src, d.Symbol)
	e.Continue = linesCmd(filepath.Join(root, filepath.FromSlash(d.Path)))
	return e, "", nil
}

// depSource reads the file of a dependency symbol from the directory
// its package is installed in. A package version is immutable, so the
// file is the one that was indexed unless the directory is gone or was
// replaced; unavailable then says which.
func (c *Ctx) depSource(d depSym, root string) (src []byte, unavailable string, err error) {
	if root == "" {
		where := "nowhere"
		if len(d.pkg.roots) > 0 {
			where = d.pkg.roots[0]
		}
		return nil, fmt.Sprintf("body unavailable: %s is no longer installed (%s); install the dependencies again",
			d.pkg.id, where), nil
	}
	f, err := c.view.s.FileByID(d.FileID)
	if err != nil {
		return nil, "", err
	}
	p := filepath.Join(root, filepath.FromSlash(d.Path))
	src, rerr := os.ReadFile(p)
	if rerr != nil || index.ContentHash(src) != f.Hash {
		return nil, fmt.Sprintf("body unavailable: %s differs from the %s that was indexed", p, d.pkg.id), nil
	}
	return src, "", nil
}

// depDocEntry is the --doc-only entry of a dependency symbol: key, span,
// signature and full doc.
func depDocEntry(d depSym) output.Entry {
	e := depEntry(d)
	e.Loc = spanLoc(d.Path, d.StartLine, d.EndLine)
	e.Doc = strings.Join(strings.Fields(d.DocText), " ")
	e.Extra["doc"] = d.DocText
	return e
}

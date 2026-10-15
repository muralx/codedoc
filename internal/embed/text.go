package embed

import (
	"fmt"
	"strings"

	"github.com/cespare/xxhash/v2"
)

// BodyLines is the number of leading body lines included in the embedding
// text of a symbol with a short doc.
const BodyLines = 10

// SymbolText composes the text embedded for a symbol whose doc is one
// passage (spec 7.7): path, container, signature, doc and the first
// BodyLines lines of the body, one per line. Empty parts are omitted so a
// top-level symbol has no blank container line.
func SymbolText(path, container, signature, doc, bodyFirstLines string) string {
	return joinParts(path, container, signature, doc, firstLines(bodyFirstLines, BodyLines))
}

// PassageText composes the text embedded for one passage of a symbol:
// the path, container and signature that say what the lines belong to,
// then the lines themselves, of its doc or of its body. For a container
// embedded by its members, the lines are their names.
func PassageText(path, container, signature, lines string) string {
	return joinParts(path, container, signature, lines)
}

// SectionText composes the text embedded for a passage of a document or
// a section (documents spec section 2): its path, its heading path (for
// a document, its title) and lines of its own text.
func SectionText(path, headings, title, own string) string {
	if headings == "" {
		headings = title
	}
	return joinParts(path, headings, own)
}

// ModuleText composes the text embedded for a documented module: its
// identity, path and doc, one per line, empty parts omitted.
func ModuleText(identity, path, doc string) string {
	return joinParts(identity, path, doc)
}

// TextHash returns a hex xxhash of s. Stored next to a vector, it tells the
// indexer whether the composed text changed and the vector must be redone.
func TextHash(s string) string {
	return fmt.Sprintf("%016x", xxhash.Sum64String(s))
}

// joinParts joins the trimmed non-empty parts with newlines.
func joinParts(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, "\n")
}

// firstLines returns at most n leading lines of s.
func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

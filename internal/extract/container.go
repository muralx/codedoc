package extract

import "strings"

// IsContainer reports whether symbols of kind nest member declarations:
// classes, structs, interfaces, traits and enums (records and Python
// classes are classes).
func IsContainer(k Kind) bool {
	switch k {
	case KindClass, KindStruct, KindInterface, KindTrait, KindEnum:
		return true
	}
	return false
}

// HashContainers sets the body hash of every container in syms to the
// hash of its signature text, the extra declaration text recorded for its
// qualified name (members that are no symbols, such as Go embedded
// interfaces or TypeScript enum members), and the signature text of each
// direct member in source order. Direct members are the symbols naming
// the container that lie inside its span, so methods a Go file declares
// outside the type body are not members. Member docs and bodies stay
// out: documenting or editing a member leaves its container's doc
// current, adding or changing a member's declaration does not (spec 7.5).
// Extractors call it before UniqueQualified.
func HashContainers(syms []Symbol, extra map[string]string) {
	members := map[string][]int{}
	for i, s := range syms {
		if s.Container != "" {
			members[s.Container] = append(members[s.Container], i)
		}
	}
	for i := range syms {
		c := &syms[i]
		if !IsContainer(c.Kind) {
			continue
		}
		var b strings.Builder
		b.WriteString(c.SigText)
		b.WriteString(sep + extra[c.Qualified])
		for _, j := range members[c.Qualified] {
			if m := syms[j]; m.Span.StartLine >= c.Span.StartLine && m.Span.EndLine <= c.Span.EndLine {
				b.WriteString(sep + m.SigText)
			}
		}
		c.BodyHash = BodyHash(b.String())
	}
}

// sep separates the parts of a container hash; BodyHash drops
// whitespace, so it must be visible.
const sep = "\x1f"

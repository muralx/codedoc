package query

import (
	"strings"

	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/store"
)

// shortName is the name text prints for a symbol in place of its stable
// key: its qualified name without the file path a TypeScript one starts
// with (AgentStore.register for src/store/agents.ts/AgentStore.register,
// a test by its title), and for a document or one of its sections its
// name or heading.
func shortName(sy store.Symbol) string {
	if sy.Qualified == "" || isDocSymbol(sy) {
		return sy.Name
	}
	head := strings.TrimSuffix(sy.Qualified, sy.Name)
	return sy.Qualified[strings.LastIndex(head, "/")+1:]
}

// subject is what the title of a section is about, by name and by
// stable key: text names it, JSON and --keys keep the key.
type subject struct{ name, key string }

// about is the subject of a title that ends with a symbol.
func about(prefix string, sy store.Symbol) subject {
	return subject{name: prefix + shortName(sy), key: prefix + sy.StableKey}
}

// plus appends the same text to both forms of a subject.
func (s subject) plus(suffix string) subject {
	return subject{name: s.name + suffix, key: s.key + suffix}
}

// tagged is the section of ranked entries whose tags are the point of
// the answer (doc states, problems, what changed): text prints them all.
func (c *Ctx) tagged(title string, entries []output.Entry) output.Section {
	sec := c.section(title, entries)
	sec.AllTags = true
	return sec
}

// titled builds the section about s from ranked entries (see section).
func (c *Ctx) titled(s subject, entries []output.Entry) output.Section {
	sec := c.section(s.name, entries)
	if s.key != s.name {
		sec.KeyTitle = s.key
	}
	return sec
}

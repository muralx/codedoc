package query

import (
	"fmt"
	"strings"

	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/store"
)

// crowdedFile is the number of call sites above which a file of an
// impact answer is summarized, and shownSites how many it then prints.
const (
	crowdedFile = 10
	shownSites  = 3
)

// callers lists the callers of the symbol as codedoc callers does, each
// with the lines of its calls, production code first and the callers of
// one file together; the calls whose target is one of several
// candidates follow under "possibly callers". A file with more than
// crowdedFile call sites prints its first shownSites and a line with
// the number of the others and the command that lists them (see crowd).
func (im *impact) callers() ([]output.Section, error) {
	c := im.c
	entries, err := c.walkEntries(im.sy, GraphOpts{Depth: 1}, true)
	if err != nil {
		return nil, err
	}
	if err := c.noteOverCap(im.sy, "call"); err != nil {
		return nil, err
	}
	sites, err := im.callSites()
	if err != nil {
		return nil, err
	}
	var sure, unsure []output.Entry
	for _, e := range entries {
		if e.Mark == possibly {
			unsure = append(unsure, e)
		} else {
			sure = append(sure, e)
		}
	}
	command := im.command("callers")
	shape := func(entries []output.Entry, offset int) []output.Entry {
		return crowd(entries, crowding{sites: sites, what: "call sites in this file", command: command, offset: offset,
			wide: c.Context.wide()})
	}
	return append(im.page("callers", command, sure, shape), im.page("possibly callers", command, unsure, shape)...), nil
}

// callSites counts, per stable key of a caller, the calls it makes to
// the symbol.
func (im *impact) callSites() (map[string]int, error) {
	edges, err := im.c.Store.EdgesTo(im.sy.ID, "calls")
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(edges))
	for i, e := range edges {
		ids[i] = e.Src
	}
	srcs, err := im.c.Store.SymbolsByID(ids)
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	for _, e := range edges {
		out[srcs[e.Src].StableKey]++
	}
	return out, nil
}

// crowding says how crowd shortens a list: what it counts (sites holds
// the call sites of each caller by stable key; an entry without a count
// is one), what they are called, the command that lists them all,
// continued from an entry with --offset, the rank of the first entry,
// and whether the fragments are wider than a line (an entry is then
// kept or left out as a whole). dir, when set, groups the entries by
// what it returns in place of their file.
type crowding struct {
	sites   map[string]int
	what    string
	command string
	offset  int
	wide    bool
	dir     func(output.Entry) string
}

// group is what the entries of one group share: their file, or what
// dir says.
func (cr crowding) group(e output.Entry) string {
	if cr.dir != nil {
		return cr.dir(e)
	}
	return fmt.Sprintf("%d:%s", e.RepoID, locPath(e.Loc))
}

// crowd shortens every group of consecutive entries (the callers of one
// file) that holds more than crowdedFile of what is counted to its
// first shownSites: the last entry kept stands for the ones left out
// and ends with their number and the command that lists them from that
// entry on.
func crowd(entries []output.Entry, cr crowding) []output.Entry {
	var out []output.Entry
	for i := 0; i < len(entries); {
		j, total := i, 0
		for j < len(entries) && cr.group(entries[j]) == cr.group(entries[i]) {
			total += max(cr.sites[entries[j].Key], 1)
			j++
		}
		if total <= crowdedFile {
			out = append(out, entries[i:j]...)
			i = j
			continue
		}
		shown, printed, next := 0, 0, i
		for k := i; k < j && printed < shownSites; k++ {
			e, n := entries[k], max(cr.sites[entries[k].Key], 1)
			next = k + 1
			if n > shownSites-printed && !cr.wide {
				n, next = shownSites-printed, k
				e = firstSites(e, n)
			}
			printed += n
			shown++
			out = append(out, e)
		}
		if last := &out[len(out)-1]; printed < total {
			last.Covers = j - i - shown + 1
			last.More = fmt.Sprintf("%d more %s: %s --offset %d", total-printed, cr.what, cr.command, cr.offset+next)
		}
		i = j
	}
	return out
}

// firstSites is a caller with the first n of the lines of its calls.
func firstSites(e output.Entry, n int) output.Entry {
	var kept []output.Fragment
	for _, f := range e.Fragments {
		if n <= 0 {
			break
		}
		lines := strings.SplitAfter(strings.TrimSuffix(f.Text, "\n"), "\n")
		if len(lines) > n {
			lines = lines[:n]
		}
		kept = append(kept, output.Fragment{Path: f.Path, Start: f.Start, End: f.Start + len(lines) - 1,
			Text: strings.TrimSuffix(strings.Join(lines, ""), "\n") + "\n"})
		n -= len(lines)
	}
	e.Fragments = kept
	return e
}

// refs lists the references to the symbol that are no calls (the uses
// of a type, instantiations, field accesses), one line each as codedoc
// refs prints them, the ambiguous ones under "possibly references".
func (im *impact) refs() ([]output.Section, error) {
	edges, err := im.c.Store.EdgesTo(im.sy.ID, "references")
	if err != nil {
		return nil, err
	}
	var kept []store.Edge
	for _, e := range edges {
		if e.RefKind != store.LinkRefKind {
			kept = append(kept, e)
		}
	}
	entries, err := im.c.sourceEntries(kept, func(store.Symbol) bool { return true })
	if err != nil {
		return nil, err
	}
	var sure, unsure []output.Entry
	for _, e := range entries {
		if e.Mark == possibly {
			unsure = append(unsure, e)
		} else {
			sure = append(sure, e)
		}
	}
	command := im.command("refs")
	shape := func(entries []output.Entry, offset int) []output.Entry {
		return crowd(entries, crowding{what: "references in this file", command: command, offset: offset, wide: true})
	}
	return append(im.page("references", command, sure, shape), im.page("possibly references", command, unsure, shape)...), nil
}

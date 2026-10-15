package query

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/output"
)

// Symbols prints several symbols in one answer, each laid out as Symbol
// prints one, under one budget: the sources that fit an equal share of
// it are whole, and the larger ones share what is left, each cut to the
// lines that fit and continued with codedoc lines, or answered by the
// outline of its members (see symbolParts). Each address is resolved as
// Symbol resolves it: one answered for its only plausible or its best
// candidate says so in the note, after the address. An address that
// names no symbol is one line with its nearest candidates and does not
// fail the others; only when no address was answered or had candidates
// the query fails with ErrNotFound.
func Symbols(c *Ctx, addrs []string, o SymbolOpts) (*output.Response, error) {
	if o.Context < 0 {
		return nil, errors.New("--context must not be negative")
	}
	targets := make([]target, len(addrs))
	failed := make([]error, len(addrs))
	lines := make([]string, len(addrs))
	var notes []string
	offered := false
	for i, addr := range addrs {
		c.notes = nil
		t, resp, err := c.target(addr, depsOnFlag)
		if err != nil && !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrAmbiguous) {
			return nil, err
		}
		targets[i], failed[i], lines[i] = t, err, missLine(addr, resp, err)
		offered = offered || err == nil || errors.Is(err, ErrNearest)
		for _, n := range c.notes {
			if err == nil {
				notes = append(notes, addr+": "+n)
			}
		}
	}
	c.notes = nil // each address that failed has its line
	parts, err := c.symbolParts(addrs, targets, lines, o)
	if err != nil {
		return nil, err
	}
	c.notes = append(notes, c.notes...)
	resp := c.share(c.symbolsResponse(parts))
	if !offered {
		return resp, fmt.Errorf("symbol %s: %w", strings.Join(addrs, " "), firstMiss(failed))
	}
	return resp, nil
}

// firstMiss is the error a query for several symbols fails with: the
// ambiguity --strict reports when there is one, else not found.
func firstMiss(failed []error) error {
	for _, err := range failed {
		if errors.Is(err, ErrAmbiguous) {
			return ErrAmbiguous
		}
	}
	return ErrNotFound
}

// nearestShown is the most candidates the line of an address that names
// no symbol lists.
const nearestShown = 5

// missLine is what the one line of an address that failed says, "" for
// one that resolved: not found, with the names of the nearest
// candidates resp lists, or what the query for it alone fails with.
func missLine(addr string, resp *output.Response, err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrAmbiguous):
		return strings.TrimPrefix(err.Error(), "symbol "+addr+": ") + "; codedoc symbol " + shellQuote(addr) + " --strict lists them"
	case !errors.Is(err, ErrNearest) || resp == nil || len(resp.Sections) == 0:
		return "not found"
	}
	var names []string
	for _, e := range resp.Sections[0].Entries {
		if name := entryName(e); len(names) < nearestShown && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return "not found; nearest: " + strings.Join(names, ", ")
}

// symbolsResponse joins the parts of a query for several symbols into
// one response: their sections in order, a header for the repos they
// draw on, and what the notes say about each, once.
func (c *Ctx) symbolsResponse(parts []symbolPart) *output.Response {
	var secs []output.Section
	var repos []*config.Repo
	var notes []string
	for _, p := range parts {
		secs = append(secs, p.sec)
		for _, r := range p.repos {
			if !slices.Contains(repos, r) {
				repos = append(repos, r)
			}
		}
		if p.note != "" && !slices.Contains(notes, p.note) {
			notes = append(notes, p.note)
		}
	}
	if len(repos) == 0 {
		repos = c.scope()
	}
	c.notes = append(notes, c.notes...)
	return c.respond(repos, secs...)
}

// outlineReserve is the room symbolParts keeps for the header and for
// the note about a symbol answered by the outline of its members, when
// it decides whether that outline fits a share of the budget.
const outlineReserve = 250

// symbolParts builds the part of each address. Every part is sized
// whole first; the ones larger than their share of the budget are built
// again under that share, which is where a symbol with members turns
// into their outline, when the outline fits the share; else, as for any
// other symbol, the renderer cuts the source to the lines that fit.
func (c *Ctx) symbolParts(addrs []string, targets []target, failed []string, o SymbolOpts) ([]symbolPart, error) {
	budget := c.Budget
	defer func() { c.Budget = budget }()
	c.Budget = 0
	parts := make([]symbolPart, len(addrs))
	sizes := make([]int, len(addrs))
	for i, addr := range addrs {
		var err error
		if parts[i], err = c.addressPart(addr, targets[i], failed[i], o); err != nil {
			return nil, err
		}
		sizes[i] = output.SectionsSize([]output.Section{parts[i].sec})
	}
	if budget == 0 {
		return parts, nil
	}
	for i, room := range shares(sizes, max(budget-outlineReserve, 1)) {
		if failed[i] != "" || sizes[i] <= room {
			continue
		}
		c.Budget = room
		noted := len(c.notes)
		p, err := c.addressPart(addrs[i], targets[i], "", o)
		if err != nil {
			return nil, err
		}
		if output.SectionsSize([]output.Section{p.sec}) > room {
			// The outline of its members does not fit either: the source cut
			// to the lines that fit says more than the start of a list.
			c.notes = c.notes[:noted]
			continue
		}
		parts[i] = p
	}
	return parts, nil
}

// addressPart is the part of one address of a query for several symbols
// under the budget of the context: the symbol t (see symbolPart), or
// the one line failed of an address that names none. Its section names
// the address and the command that prints it alone.
func (c *Ctx) addressPart(addr string, t target, failed string, o SymbolOpts) (symbolPart, error) {
	p := symbolPart{sec: oneSymbol(output.Entry{Key: addr, Name: addr, Text: failed, Extra: map[string]any{"unresolved": true}})}
	if failed == "" {
		var err error
		if p, err = c.symbolPart(t, o); err != nil {
			return p, err
		}
	}
	p.sec.Title, p.sec.Command = "symbol "+addr, c.symbolCommand(addr)
	return p, nil
}

// symbolCommand is the command that prints one of the symbols of the
// query alone: the command that ran, with that address for its only one.
func (c *Ctx) symbolCommand(addr string) string {
	words := strings.Fields(c.Command)
	flags := slices.IndexFunc(words, func(w string) bool { return strings.HasPrefix(w, "--") })
	if flags < 0 {
		return "codedoc symbol " + shellQuote(addr)
	}
	return strings.Join(append([]string{"codedoc symbol", shellQuote(addr)}, words[flags:]...), " ")
}

package query

import (
	"github.com/muralx/codedoc/internal/output"
)

// share splits the budget of an answer made of several questions among
// its sections, so that none starves the others: every section that
// fits in an equal share of what is left is given its size, and the
// ones that do not split the rest equally (see shares). The renderer
// hands the room a section leaves unused to the sections after it.
// Without a budget every section is whole.
func (c *Ctx) share(resp *output.Response) *output.Response {
	if c.Budget <= 0 || len(resp.Sections) == 0 {
		return resp
	}
	room := c.Budget - len(output.HeaderLine(resp.Header))
	if resp.Note != "" {
		room -= len(resp.Note) + 1
	}
	// Every section is sure of its title and of the line that names the
	// command for what was cut, when the budget holds that for them all;
	// the rest of the room is what is shared.
	sizes := output.SectionSizes(resp)
	floors := make([]int, len(sizes))
	held := 0
	for i, sec := range resp.Sections {
		command := sec.Command
		if command == "" {
			command = resp.Command
		}
		floors[i] = min(sizes[i], len(sec.Title)+len(command)+floorSlack)
		held += floors[i]
	}
	if held > room {
		floors, held = make([]int, len(sizes)), 0
	}
	for i := range sizes {
		sizes[i] -= floors[i]
	}
	for i, r := range shares(sizes, room-held) {
		resp.Sections[i].Room = floors[i] + r
	}
	return resp
}

// floorSlack is the room share keeps, besides its title and command,
// for the line that ends a cut section ("== (1234)", "1234 more:
// --offset 1234").
const floorSlack = 40

// shares gives each of the sizes its part of room: a size that fits in
// an equal share of the room still open is granted whole, and what it
// leaves is shared by the others; the sizes that never fit get an equal
// share of the rest, at least one byte.
func shares(sizes []int, room int) []int {
	out := make([]int, len(sizes))
	open := make([]int, len(sizes))
	for i := range sizes {
		open[i] = i
	}
	for len(open) > 0 {
		fair := max(room/len(open), 1)
		var over []int
		for _, i := range open {
			if sizes[i] <= fair {
				out[i] = max(sizes[i], 1)
				room -= sizes[i]
			} else {
				over = append(over, i)
			}
		}
		if len(over) == len(open) {
			for _, i := range over {
				out[i] = fair
			}
			break
		}
		open = over
	}
	return out
}

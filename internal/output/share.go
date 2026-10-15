package output

import "math"

// frame returns what surrounds the entries of a section: its title line
// (none for a section without one), which counts every ranked entry,
// including those skipped by the offset and the held ones the caller
// kept back; the worst-case length of its more line; and that of the
// line ending it when it is cut.
func (t *textWriter) frame(s Section, held int) (title string, more, cut int) {
	if all := covered(s.Entries) + held; all > 0 {
		more = len(t.moreLine(s, all, s.Offset+covered(s.Entries)))
		cut = max(more, len(t.tooSmallLine(s, all)))
	}
	if !s.NoTitle {
		title = t.title(s)
	}
	return title, more, cut
}

// entriesSize is the number of bytes entries take written whole.
func entriesSize(entries []Entry, summary bool) int {
	n := 0
	for _, e := range entries {
		n += len(entryText(e, true, summary))
	}
	return n
}

// omit writes the line that ends a section of which only the first n
// entries were written, held more being with the caller: how many ranked
// entries are left and the command that prints them.
func (t *textWriter) omit(s Section, n, held int) {
	omitted := covered(s.Entries[n:]) + held
	line := t.moreLine(s, omitted, s.Offset+covered(s.Entries[:n]))
	if n == 0 && len(s.Entries) > 0 {
		line = t.tooSmallLine(s, omitted) // --offset would repeat this very answer
	}
	t.b.WriteString(line)
	t.omitted += omitted
}

// SectionSizes returns, for each section of r, the bytes its text
// layout takes without a budget: what an answer of several questions
// shares its budget by (see Section.Room).
func SectionSizes(r *Response) []int {
	out := make([]int, len(r.Sections))
	for i, s := range r.Sections {
		t := textWriter{limit: math.MaxInt, keys: r.Keys, detail: r.Detail, command: r.Command}
		s.Room = 0
		t.sections([]Section{s}, 0)
		out[i] = t.b.Len()
	}
	return out
}

// share lowers the limit to the room of section s, whose title of title
// bytes is about to be written: its share of the budget and what the
// sections before it spared, at least the title and the cut bytes of
// the line ending it when it is cut, with rest bytes to follow it. It
// reports whether the section is whole within that room.
func (t *textWriter) share(s Section, spare, title, cut, rest int) bool {
	t.limit = min(t.limit, t.b.Len()+max(s.Room+spare, title+cut)+rest)
	return s.held() == 0 && t.fits(title+entriesSize(s.Entries, t.summary)+rest)
}

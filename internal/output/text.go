package output

import (
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// textWriter builds the compact text layout within limit bytes. Before
// anything optional is written it checks that the lines that may still
// have to follow (a "more" line, the summary of later sections, the note)
// fit as well, so the output never exceeds the limit.
type textWriter struct {
	b       strings.Builder
	limit   int
	omitted int
	// summary marks the stdout summary of an answer written in full to a
	// file (--out): omission lines point at that file, not at --out.
	summary bool
	// head is the length of the header line: a builder of that length
	// holds nothing of the answer yet.
	head int
	// keys and detail are Response.Keys and Response.Detail; command is
	// Response.Command.
	keys, detail bool
	command      string
	// resp is the answer being written and whole the size of its text
	// without a budget, once an omission line needed it (see rerun).
	resp  *Response
	whole int
	// wrote is the number of entries written.
	wrote int
}

// renderText writes a header line, then per section a title line, one
// line per entry with optional numbered body lines, and a "more" line
// when entries were omitted. Sections after a cut one collapse into one
// summary line. The note comes last.
func renderText(w io.Writer, r *Response, budget int) (int, error) {
	return writeText(w, r, budget, false)
}

// RenderSummary writes the text layout of r within budget as the summary
// of an answer whose full result went to a file: omission lines say the
// rest is in that file instead of suggesting --offset or --out.
func RenderSummary(w io.Writer, r *Response, budget int) (int, error) {
	return writeText(w, r, budget, true)
}

func writeText(w io.Writer, r *Response, budget int, summary bool) (int, error) {
	noteLen := 0
	if r.Note != "" {
		noteLen = len(r.Note) + 1
	}
	t := newTextWriter(r, budget, summary)
	t.sections(r.Sections, noteLen)
	if t.wrote == 0 && noteLen > 0 && t.omitted > 0 {
		// The note left no room for one entry: the answer comes first, and
		// the note gets what it leaves.
		t = newTextWriter(r, budget, summary)
		t.sections(r.Sections, 0)
	}
	t.note(r.Note)
	_, err := io.WriteString(w, t.b.String())
	return t.omitted, err
}

// newTextWriter starts the text of r under budget with its header.
func newTextWriter(r *Response, budget int, summary bool) *textWriter {
	t := &textWriter{limit: math.MaxInt, summary: summary, keys: r.Keys, detail: r.Detail, command: r.Command, resp: r}
	if budget > 0 {
		t.limit = budget
	}
	if !r.Bare {
		t.b.WriteString(HeaderLine(r.Header))
	}
	t.head = t.b.Len()
	return t
}

// title is the title line of a section: its title, with stable keys
// when they are asked for, and the number of its ranked entries.
func (t *textWriter) title(s Section) string {
	name := s.Title
	if t.keys {
		name = s.keyTitle()
	}
	return fmt.Sprintf("== %s (%d)\n", name, s.count())
}

// fits reports whether n more bytes stay within the limit.
func (t *textWriter) fits(n int) bool { return t.b.Len()+n <= t.limit }

func (t *textWriter) sections(secs []Section, noteLen int) {
	spare := 0 // room the sections with a share of the budget left unused
	for i, s := range secs {
		s.Entries = t.display(s)
		rest := len(t.summaryLine(secs[i+1:])) + noteLen
		held := s.held()
		title, more, cut := t.frame(s, held)
		// A section with a share that fits whole needs no room for the
		// line that would end it cut.
		whole := s.Room > 0 && held == 0 && t.fits(len(title)+entriesSize(s.Entries, t.summary)+rest)
		if !whole && !t.fits(len(title)+cut+rest) {
			t.collapse(secs[i:])
			return
		}
		start, limit := t.b.Len(), t.limit
		if s.Room > 0 && t.share(s, spare, len(title), cut, rest) {
			more = 0 // the section is whole within its share: no line follows its entries
		}
		t.b.WriteString(title)
		n := t.entries(s.Entries, more, rest, held > 0)
		if t.limit = limit; n < len(s.Entries) || held > 0 {
			t.omit(s, n, held)
		}
		if s.Room > 0 {
			spare = max(0, s.Room+spare-(t.b.Len()-start))
		} else if n < len(s.Entries) {
			t.collapse(secs[i+1:])
			return
		}
	}
}

// entries writes entries while each one leaves room for rest, and for
// the more line unless it is the last entry of a section that ends with
// it (follows is false). An entry whose body does not fit is written
// with the lines of it that fit when it can be continued (see cutEntry),
// else without it: with the command that prints it, or the tag of a
// body no command prints. The first entry whose line alone does not fit
// ends the section. It returns the number of entries written.
func (t *textWriter) entries(entries []Entry, more, rest int, follows bool) int {
	for i, e := range entries {
		reserve := rest
		if follows || i < len(entries)-1 {
			reserve += more
		}
		text := entryText(e, true, t.summary)
		if !t.fits(len(text) + reserve) {
			text = t.shortened(e, reserve)
		}
		if !t.fits(len(text) + reserve) {
			return i
		}
		t.b.WriteString(text)
		t.wrote++
	}
	return len(entries)
}

// shortened is e without the lines of its body that do not fit with
// reserve bytes to follow: cut to those that do when it can be
// continued, else without its body.
func (t *textWriter) shortened(e Entry, reserve int) string {
	if cut, ok := cutEntry(e, t.limit-t.b.Len()-reserve); ok && !t.summary {
		return cut
	}
	return entryText(e, false, t.summary)
}

// collapse replaces secs by one summary line and counts their entries as
// omitted. When the summary does not fit and nothing of the answer was
// written yet, the title of the first section is tried with a line
// counting its entries (see titled), so an answer cut to nothing still
// says what it is about; then the shorter count-only line, and when not
// even that fits, the shortest line counting what was left out, over the
// budget: omitted sections are never dropped silently.
func (t *textWriter) collapse(secs []Section) {
	n := 0
	for _, s := range secs {
		n += covered(s.Entries) + s.held()
	}
	t.omitted += n
	if len(secs) == 0 {
		return
	}
	lines := []string{t.summaryLine(secs)}
	if t.b.Len() == t.head {
		lines = append(lines, t.titled(secs)...)
	}
	for _, line := range append(lines, t.countLine(secs)) {
		if t.fits(len(line)) {
			t.b.WriteString(line)
			return
		}
	}
	_, _ = fmt.Fprintf(&t.b, "... %d more\n", max(n, len(secs)))
}

// titled renders sections of which no entry fits as the title of the
// first one, the line counting its entries with the command that prints
// them (a shorter one too when no command is known) and the count of
// the sections after it.
func (t *textWriter) titled(secs []Section) []string {
	s := secs[0]
	n := covered(s.Entries) + s.held()
	if n == 0 {
		return nil
	}
	title, rest := t.title(s), t.countLine(secs[1:])
	lines := []string{title + t.tooSmallLine(s, n) + rest}
	if !t.summary && s.Command == "" && t.command == "" {
		lines = append(lines, title+fmt.Sprintf("... %d more; raise --budget\n", n)+rest)
	}
	return lines
}

// note writes the note, or the whole sentences and candidates of it
// that fit (see fitNote): never a part of one.
func (t *textWriter) note(note string) {
	if note = fitNote(note, t.limit-t.b.Len()-1); note != "" {
		t.b.WriteString(note + "\n")
	}
}

// SectionsSize is the number of bytes the text layout of secs takes
// without a budget, header and note excluded.
func SectionsSize(secs []Section) int {
	t := textWriter{limit: math.MaxInt}
	t.sections(secs, 0)
	return t.b.Len()
}

// entryText renders one display entry (see textWriter.display): the
// path line of its file group when it opens one, its line, its fragments
// and its body. withBody false leaves the body out: one that can be
// continued is replaced by the command that prints it, any other (and
// every one in a summary) by a tag carrying its line range.
func entryText(e Entry, withBody, summary bool) string {
	named := !withBody && !summary && continuable(e)
	omitted := ""
	if e.Body != "" && !withBody && !named {
		hint := "; use --out"
		if summary {
			hint = ""
		}
		omitted = fmt.Sprintf("body omitted: lines %d-%d%s", e.BodyStart, bodyEnd(e), hint)
	}
	line := entryLine(e, omitted)
	switch {
	case named:
		return line + continueLine(e, e.BodyStart)
	case e.Body == "" || !withBody:
		return line
	case e.Plain:
		return line + strings.TrimSuffix(e.Body, "\n") + "\n"
	}
	return line + numberLines(e.Body, e.BodyStart)
}

// entryLine is the line of an entry with its tags, and extra for one
// more when it is set, then its fragments and the line that follows
// them.
func entryLine(e Entry, extra string) string {
	var parts []string
	for _, p := range []string{e.Key, e.Loc, e.Text, e.Doc} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	tags := make([]string, 0, len(e.Tags)+1)
	for _, t := range e.Tags {
		tags = append(tags, "["+t+"]")
	}
	if extra != "" {
		tags = append(tags, "["+extra+"]")
	}
	if len(tags) > 0 {
		parts = append(parts, strings.Join(tags, " "))
	}
	indent := strings.Repeat("  ", e.Depth)
	line := e.lead + indent + strings.Join(parts, "  ") + "\n" + fragmentLines(e.Fragments)
	if e.More != "" {
		line += indent + e.More + "\n"
	}
	return line
}

// fragmentLines renders fragments as numbered lines, each under the
// location of its file when that is not the file of the entry.
func fragmentLines(fs []Fragment) string {
	var b strings.Builder
	for _, f := range fs {
		if f.Path != "" {
			_, _ = fmt.Fprintf(&b, "%s:%d-%d\n", f.Path, f.Start, f.End)
		}
		b.WriteString(numberLines(f.Text, f.Start))
	}
	return b.String()
}

// numberLines prefixes each body line with its line number and a tab.
func numberLines(body string, start int) string {
	var b strings.Builder
	for i, l := range strings.Split(strings.TrimSuffix(body, "\n"), "\n") {
		b.WriteString(strconv.Itoa(start + i))
		b.WriteByte('\t')
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return b.String()
}

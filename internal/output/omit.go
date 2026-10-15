package output

import (
	"fmt"
	"regexp"
	"strings"
)

// budgetFlag matches the budget of a command line.
var budgetFlag = regexp.MustCompile(` --budget[ =]\S+`)

// wholeSlack is added to the size of an answer to get the budget that
// holds it whole: the room the renderer keeps for the lines it then
// does not write.
const wholeSlack = 400

// withBudget is command asking for a budget that holds size bytes,
// rounded up to a hundred.
func withBudget(command string, size int) string {
	return fmt.Sprintf("%s --budget %d", budgetFlag.ReplaceAllString(command, ""), (size+wholeSlack+99)/100*100)
}

// atOffset is command continued at ranked entry next.
func atOffset(command string, next int) string {
	if next == 0 {
		return command
	}
	return fmt.Sprintf("%s --offset %d", command, next)
}

// wholeText is the size of the text layout of r without a budget.
func wholeText(r *Response) int {
	n := len(r.Note) + 1
	if !r.Bare {
		n += len(HeaderLine(r.Header))
	}
	for _, size := range SectionSizes(r) {
		n += size
	}
	return n
}

// rerun is the command that prints, from ranked entry next on, what a
// budget too small for one entry left out: the command of the answer
// with the budget that holds it whole. It is empty when the command is
// not known.
func (t *textWriter) rerun(next int) string {
	if t.command == "" || t.resp == nil {
		return ""
	}
	if t.whole == 0 {
		t.whole = wholeText(t.resp)
	}
	return atOffset(withBudget(t.command, t.whole), next)
}

// tooSmallLine replaces the more line when not one entry of a section
// fit: the command of the section, else the command of the answer with
// a budget that holds it (continuing at the same offset would repeat
// this very answer).
func (t *textWriter) tooSmallLine(s Section, omitted int) string {
	switch {
	case t.summary:
		return fmt.Sprintf("... %d more in the full result\n", omitted)
	case s.Command != "":
		return t.moreLine(s, omitted, s.Offset)
	}
	if command := t.rerun(s.Offset); command != "" {
		return fmt.Sprintf("%d more: %s\n", omitted, command)
	}
	return fmt.Sprintf("... %d more; budget too small for one entry; raise --budget or use --out FILE\n", omitted)
}

// moreLine says how many entries of a section were omitted and how to
// get them: the command of the section, else the one of the answer,
// continued at entry next.
func (t *textWriter) moreLine(s Section, omitted, next int) string {
	if t.summary {
		return fmt.Sprintf("... %d more in the full result\n", omitted)
	}
	command := t.command
	if s.Command != "" {
		command = s.Command
	}
	if command == "" {
		return fmt.Sprintf("%d more: rerun with --offset %d\n", omitted, next)
	}
	return fmt.Sprintf("%d more: %s\n", omitted, atOffset(command, next))
}

// maxSummaryTitles bounds the bytes of section titles a summary line
// lists; later titles are counted instead.
const maxSummaryTitles = 160

// summaryLine names sections that were not printed and the command
// that prints them; empty when none. It lists titles while they fit in
// maxSummaryTitles bytes and counts the rest, so its length stays
// bounded however many sections were cut.
func (t *textWriter) summaryLine(secs []Section) string {
	if len(secs) == 0 {
		return ""
	}
	var titles []string
	size := 0
	for _, s := range secs {
		if size+len(s.Title)+2 > maxSummaryTitles {
			break
		}
		titles = append(titles, s.Title)
		size += len(s.Title) + 2
	}
	list := strings.Join(titles, ", ")
	if rest := len(secs) - len(titles); rest > 0 {
		if list != "" {
			list += ", "
		}
		list += fmt.Sprintf("+%d more", rest)
	}
	head := fmt.Sprintf("... %d more %s", len(secs), plural(len(secs), "section", "sections"))
	switch command := t.collapsed(secs); {
	case t.summary:
		return fmt.Sprintf("%s (titles: %s); in the full result\n", head, list)
	case command != "":
		return fmt.Sprintf("%s (%s): %s\n", head, list, command)
	}
	return fmt.Sprintf("%s (titles: %s); rerun with --out FILE\n", head, list)
}

// collapsed is the command that prints sections that were not printed:
// the command of the only one when it has its own, else the command of
// the answer with a budget that holds it. It is empty when no command
// is known.
func (t *textWriter) collapsed(secs []Section) string {
	if len(secs) == 1 && secs[0].Command != "" {
		return atOffset(secs[0].Command, secs[0].Offset)
	}
	return t.rerun(secs[0].Offset)
}

// countLine is the shortest summary of sections that were not printed.
func (t *textWriter) countLine(secs []Section) string {
	if len(secs) == 0 {
		return ""
	}
	head := fmt.Sprintf("... %d more %s", len(secs), plural(len(secs), "section", "sections"))
	switch command := t.collapsed(secs); {
	case t.summary:
		return head + "; in the full result\n"
	case command != "":
		return fmt.Sprintf("%s: %s\n", head, command)
	}
	return head + "; use --out FILE\n"
}

// plural returns one when n is 1, else many.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

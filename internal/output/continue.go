package output

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// continuable reports whether the body of e may be cut to the lines
// that fit: it has numbered lines and a command that prints the rest.
func continuable(e Entry) bool { return e.Continue != "" && e.Body != "" && !e.Plain }

// continueCmd is the command that prints the body of e from line next
// to its end.
func continueCmd(e Entry, next int) string {
	return fmt.Sprintf("%s %d-%d", e.Continue, next, bodyEnd(e))
}

// continueLine ends a body cut before line next: how many lines are
// left and the exact command that prints them.
func continueLine(e Entry, next int) string {
	return fmt.Sprintf("%d more lines: %s\n", bodyEnd(e)-next+1, continueCmd(e, next))
}

// cutEntry renders e with the first lines of its body that fit in room
// bytes together with the continue line; ok is false when e cannot be
// continued or not one line fits.
func cutEntry(e Entry, room int) (text string, ok bool) {
	if !continuable(e) {
		return "", false
	}
	head := e
	head.Body = ""
	var b strings.Builder
	b.WriteString(entryText(head, true, false))
	lines := strings.Split(strings.TrimSuffix(e.Body, "\n"), "\n")
	kept, keptLen := 0, 0
	for i, l := range lines[:len(lines)-1] {
		b.WriteString(strconv.Itoa(e.BodyStart + i))
		b.WriteByte('\t')
		b.WriteString(l)
		b.WriteByte('\n')
		if b.Len() > room {
			break
		}
		if b.Len()+len(continueLine(e, e.BodyStart+i+1)) <= room {
			kept, keptLen = i+1, b.Len()
		}
	}
	if kept == 0 {
		return "", false
	}
	return b.String()[:keptLen] + continueLine(e, e.BodyStart+kept), true
}

// cutJSON returns the entry of se with the most leading lines of its
// body whose extra bytes over the entry without body fit in room, and
// the command that continues it; ok is false when se cannot be
// continued or not one line fits.
func cutJSON(se *sizedEntry, room int) (cut jsonEntry, extra int, ok bool) {
	if !continuable(se.entry) {
		return jsonEntry{}, 0, false
	}
	lines := strings.SplitAfter(strings.TrimSuffix(se.full.Body, "\n"), "\n")
	build := func(k int) (jsonEntry, int) {
		e := se.full
		e.Body = strings.TrimSuffix(strings.Join(lines[:k], ""), "\n") + "\n"
		e.BodyEnd = e.BodyStart + k - 1
		e.Continue = continueCmd(se.entry, e.BodyStart+k)
		n, err := jsonLen(e)
		if err != nil {
			return e, room + se.shortLen + 1 // never fits
		}
		return e, n - se.shortLen
	}
	// The size grows with the number of lines: find the first count
	// that does not fit.
	k := sort.Search(len(lines)-1, func(i int) bool {
		_, n := build(i + 1)
		return n > room
	})
	if k == 0 {
		return jsonEntry{}, 0, false
	}
	cut, extra = build(k)
	return cut, extra, true
}

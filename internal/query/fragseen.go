package query

import (
	"strings"

	"github.com/muralx/codedoc/internal/output"
)

// fileRef names a file of the workspace index across the hits of one
// answer.
type fileRef struct{ repo, file int64 }

// shownLines records the lines of each file an answer already printed
// as fragments, so a later hit does not print them again: a section and
// the sections nested in it, or a type and its members, match the same
// lines.
type shownLines map[fileRef][]lineSpan

// unseen returns the fragments of a hit in file f without the lines an
// earlier hit of the answer printed, and records what is left. A
// fragment loses the shown lines at its start and at its end; one that
// keeps less than half of its lines is dropped, since what remains is
// context of lines the reader has.
func (s shownLines) unseen(f fileRef, frags []output.Fragment) []output.Fragment {
	var out []output.Fragment
	for _, fr := range frags {
		first, last := fr.Start, fr.End
		for first <= last && inSpans(s[f], first) {
			first++
		}
		for last >= first && inSpans(s[f], last) {
			last--
		}
		if kept, all := last-first+1, fr.End-fr.Start+1; kept*2 < all {
			continue
		}
		if first != fr.Start || last != fr.End {
			lines := strings.Split(strings.TrimSuffix(fr.Text, "\n"), "\n")
			fr.Text = strings.Join(lines[first-fr.Start:last-fr.Start+1], "\n") + "\n"
			fr.Start, fr.End = first, last
		}
		s[f] = append(s[f], lineSpan{fr.Start, fr.End})
		out = append(out, fr)
	}
	return out
}

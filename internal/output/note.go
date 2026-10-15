package output

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// noteJoin separates the sentences of a note; alsoLead starts the list
// of the other candidates of an address, itemJoin separates them.
const (
	noteJoin = ". "
	alsoLead = "also: "
	itemJoin = ", "
)

// moreItems matches the item of a list that counts the ones left out.
var moreItems = regexp.MustCompile(`^\+(\d+) more$`)

// fitNote returns the part of note that fits in room bytes, never cut
// inside a word or a command: its sentences are kept whole, in order,
// while they fit; of the first that does not, a list of candidates
// ("also: A (loc), B (loc)") keeps the candidates that fit and counts
// the others ("+N more"), and any other sentence is left out with
// everything after it.
func fitNote(note string, room int) string {
	if len(note) <= room {
		return note
	}
	var kept []string
	used := 0
	for _, s := range strings.Split(note, noteJoin) {
		sep := 0
		if len(kept) > 0 {
			sep = len(noteJoin)
		}
		if used+sep+len(s) > room {
			if short := fitItems(s, room-used-sep); short != "" {
				kept = append(kept, short)
			}
			break
		}
		kept = append(kept, s)
		used += sep + len(s)
	}
	return strings.Join(kept, noteJoin)
}

// fitItems shortens a sentence that lists candidates to room bytes by
// dropping candidates from its end and counting them; it is "" for any
// other sentence and when not one candidate fits.
func fitItems(s string, room int) string {
	at := strings.Index(s, alsoLead)
	if at < 0 {
		return ""
	}
	head := s[:at+len(alsoLead)]
	items := strings.Split(s[len(head):], itemJoin)
	dropped := 0
	if m := moreItems.FindStringSubmatch(items[len(items)-1]); m != nil {
		dropped, _ = strconv.Atoi(m[1])
		items = items[:len(items)-1]
	}
	for n := len(items) - 1; n > 0; n-- {
		short := fmt.Sprintf("%s%s%s+%d more", head, strings.Join(items[:n], itemJoin), itemJoin, dropped+len(items)-n)
		if len(short) <= room {
			return short
		}
	}
	return ""
}

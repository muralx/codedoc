package output

import (
	"encoding/json"
	"io"
	"math"
)

// jsonHeader is the JSON header: repo, branch and commit for a one-repo
// answer, workspace and repos for a workspace-wide one.
type jsonHeader struct {
	Repo      string         `json:"repo,omitempty"`
	Branch    *string        `json:"branch,omitempty"`
	Commit    *string        `json:"commit,omitempty"`
	Dirty     int            `json:"dirty"`
	Fresh     bool           `json:"fresh"`
	Workspace string         `json:"workspace,omitempty"`
	Repos     []jsonRepoHead `json:"repos,omitempty"`
	Stale     []string       `json:"stale,omitempty"`
}

type jsonRepoHead struct {
	Name   string `json:"name"`
	Branch string `json:"branch"`
	Commit string `json:"commit"`
	Dirty  int    `json:"dirty"`
	Fresh  bool   `json:"fresh"`
}

// toJSONHeader converts the header of r: a one-repo header carries repo,
// branch and commit; a workspace-wide one its workspace and repos
// instead. A bare response has none.
func toJSONHeader(r *Response) *jsonHeader {
	if r.Bare {
		return nil
	}
	h := r.Header
	out := &jsonHeader{Dirty: h.Dirty, Fresh: h.Fresh, Stale: h.Stale}
	if h.Workspace == "" {
		out.Repo, out.Branch, out.Commit = h.Repo, &h.Branch, &h.Commit
		return out
	}
	out.Workspace = h.Workspace
	for _, r := range h.Repos {
		out.Repos = append(out.Repos, jsonRepoHead(r))
	}
	return out
}

type jsonEntry struct {
	Key         string   `json:"key"`
	Name        string   `json:"name,omitempty"`
	Loc         string   `json:"loc,omitempty"`
	Text        string   `json:"text,omitempty"`
	Doc         string   `json:"doc,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Body        string   `json:"body,omitempty"`
	BodyStart   int      `json:"body_start,omitempty"`
	BodyEnd     int      `json:"body_end,omitempty"`
	BodyOmitted bool     `json:"body_omitted,omitempty"`
	// Continue is the command that prints the rest of a body that was
	// cut to the lines that fit; BodyEnd is then the last line kept.
	Continue  string     `json:"continue,omitempty"`
	Depth     int        `json:"depth,omitempty"`
	Fragments []Fragment `json:"fragments,omitempty"`
	// Covers and More are set on an entry that stands for several (see
	// Entry.Covers).
	Covers int            `json:"covers,omitempty"`
	More   string         `json:"more,omitempty"`
	Extra  map[string]any `json:"extra,omitempty"`
}

type jsonSection struct {
	Title   string      `json:"title"`
	Entries []jsonEntry `json:"entries"`
	Omitted int         `json:"omitted"`
	// NextOffset is the --offset that continues the section; set only
	// when entries were omitted.
	NextOffset *int `json:"next_offset,omitempty"`
	// Continue is the exact command that prints the omitted entries,
	// when the command of the answer or of the section is known.
	Continue string `json:"continue,omitempty"`
}

type jsonResponse struct {
	Header   *jsonHeader   `json:"header,omitempty"`
	Sections []jsonSection `json:"sections"`
	Note     string        `json:"note,omitempty"`
}

// sizedEntry is an entry with its marshalled sizes with and without body.
type sizedEntry struct {
	entry       Entry
	short, full jsonEntry
	shortLen    int
	fullLen     int
	// cut, when set, is the entry with the lines of its body that fit
	// (see cutJSON); it is written in place of short.
	cut *jsonEntry
}

// renderJSON writes the response as one JSON object and a newline within
// budget. Entries are kept in rank order without bodies while they fit;
// the first one that does not fit ends its section and every later
// section keeps no entries. Bodies are then restored in order while they
// fit. Each entry is measured once, against an upper bound of the
// skeleton that assumes every section reports omissions.
func renderJSON(w io.Writer, r *Response, budget int) (int, error) {
	sized, err := measure(r)
	if err != nil {
		return 0, err
	}
	resp := jsonResponse{Header: toJSONHeader(r), Note: r.Note, Sections: make([]jsonSection, len(r.Sections))}
	for i, s := range r.Sections {
		resp.Sections[i] = jsonSection{Title: s.keyTitle(), Entries: []jsonEntry{}}
	}
	room := math.MaxInt
	if budget > 0 {
		room, err = skeletonRoom(r, budget)
		if err != nil {
			return 0, err
		}
	}
	keep := pickEntries(sized, room)
	total := fill(&resp, r, sized, keep, wholeJSON(sized, budget-room))
	out, err := json.Marshal(resp)
	if err != nil {
		return 0, err
	}
	_, err = w.Write(append(out, '\n'))
	return total, err
}

// measure marshals every entry once in its short and full form.
func measure(r *Response) ([][]sizedEntry, error) {
	out := make([][]sizedEntry, len(r.Sections))
	for i, s := range r.Sections {
		for _, e := range s.Entries {
			full := jsonEntry{Key: e.Key, Name: e.Name, Loc: e.Loc, Text: e.Text, Doc: e.Doc, Tags: e.Tags, Depth: e.Depth,
				Fragments: e.Fragments, Covers: e.Covers, More: e.More, Extra: e.Extra}
			if e.Body != "" {
				full.Body, full.BodyStart, full.BodyEnd = e.Body, e.BodyStart, bodyEnd(e)
			}
			se := sizedEntry{entry: e, short: full, full: full}
			if e.Body != "" {
				se.short.Body, se.short.BodyOmitted = "", true
			}
			if continuable(e) {
				se.short.Continue = continueCmd(e, e.BodyStart)
			}
			var err error
			if se.shortLen, err = jsonLen(se.short); err != nil {
				return nil, err
			}
			if se.fullLen, err = jsonLen(se.full); err != nil {
				return nil, err
			}
			out[i] = append(out[i], se)
		}
	}
	return out, nil
}

func jsonLen(v any) (int, error) {
	b, err := json.Marshal(v)
	return len(b), err
}

// skeletonRoom returns the bytes left for entries once the response
// without entries, with worst-case omission fields and the trailing
// newline, is accounted for. It is negative when even the skeleton does
// not fit; the skeleton is then written alone.
func skeletonRoom(r *Response, budget int) (int, error) {
	sk := jsonResponse{Header: toJSONHeader(r), Note: r.Note}
	for _, s := range r.Sections {
		next := s.Offset + covered(s.Entries)
		sk.Sections = append(sk.Sections, jsonSection{Title: s.keyTitle(), Entries: []jsonEntry{},
			Omitted: covered(s.Entries) + s.held(), NextOffset: &next, Continue: sectionContinue(r, s, next, 0, math.MaxInt32)})
	}
	n, err := jsonLen(sk)
	return budget - n - 1, err
}

// pickEntries decides, per section, how many entries are kept and which
// of them keep their body, spending at most room bytes (each entry is
// charged one extra byte for its separating comma).
func pickEntries(sized [][]sizedEntry, room int) [][]bool {
	keep := make([][]bool, len(sized))
	var kept []*bool
	var sizes []*sizedEntry
	for i, es := range sized {
		keep[i] = make([]bool, 0, len(es)) // full capacity: pointers into it stay valid
		for j := range es {
			if es[j].shortLen+1 > room {
				return withBodies(keep, kept, sizes, room)
			}
			room -= es[j].shortLen + 1
			keep[i] = append(keep[i], false)
			kept = append(kept, &keep[i][len(keep[i])-1])
			sizes = append(sizes, &es[j])
		}
	}
	return withBodies(keep, kept, sizes, room)
}

// withBodies marks kept entries, in order, to keep their body while the
// extra bytes fit in room. A body that does not fit and can be continued
// keeps the lines that do (see cutJSON).
func withBodies(keep [][]bool, kept []*bool, sizes []*sizedEntry, room int) [][]bool {
	for k, e := range sizes {
		if extra := e.fullLen - e.shortLen; extra <= room {
			room -= extra
			*kept[k] = true
			continue
		}
		if cut, extra, ok := cutJSON(e, room); ok {
			room -= extra
			e.cut = &cut
		}
	}
	return keep
}

// fill copies the picked entries into resp and records omissions, with
// the command that prints them (whole is the size of the answer without
// a budget); it returns the total number of omitted entries.
func fill(resp *jsonResponse, r *Response, sized [][]sizedEntry, keep [][]bool, whole int) int {
	total := 0
	for i, s := range r.Sections {
		js := &resp.Sections[i]
		for j, withBody := range keep[i] {
			e := sized[i][j].short
			switch {
			case withBody:
				e = sized[i][j].full
			case sized[i][j].cut != nil:
				e = *sized[i][j].cut
			}
			js.Entries = append(js.Entries, e)
		}
		if js.Omitted = covered(s.Entries[len(keep[i]):]) + s.held(); js.Omitted > 0 {
			next := s.Offset + covered(s.Entries[:len(keep[i])])
			js.NextOffset = &next
			js.Continue = sectionContinue(r, s, next, len(keep[i]), whole)
			total += js.Omitted
		}
	}
	return total
}

// sectionContinue is the command that prints the entries of s from
// ranked entry next on, kept of them being written: the command of the
// section, else the one of the answer, which asks for a budget holding
// whole bytes when not one entry was kept (the same offset would repeat
// this very answer). It is empty when no command is known.
func sectionContinue(r *Response, s Section, next, kept, whole int) string {
	switch {
	case s.Command != "":
		return atOffset(s.Command, next)
	case r.Command == "":
		return ""
	case kept == 0 && len(s.Entries) > 0:
		return atOffset(withBudget(r.Command, whole), next)
	}
	return atOffset(r.Command, next)
}

// wholeJSON is the size of the answer with every entry and body, its
// skeleton taking skeleton bytes.
func wholeJSON(sized [][]sizedEntry, skeleton int) int {
	for _, es := range sized {
		for _, e := range es {
			skeleton += e.fullLen + 1
		}
	}
	return skeleton
}

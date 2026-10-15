package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/muralx/codedoc/internal/feedback"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/query"
)

// feedbackLineText is the most bytes of a report's text its list line
// shows, so that a line always fits the default budget; --format json
// carries the whole text.
const feedbackLineText = 400

// listFeedback prints the reports filed, oldest first, as one section
// without a header (the list describes no index): one line each, cut by
// --budget and continued with --offset like a query answer, written
// whole to --out.
func listFeedback(w io.Writer, f feedbackFlags) error {
	if f.offset < 0 || f.budget < 0 {
		return errors.New("--offset and --budget must not be negative")
	}
	var since time.Time
	if f.since != "" {
		at, ok := query.TimeBound(f.since, time.Now())
		if !ok {
			return fmt.Errorf("--since %q: not a duration (7d, 3h) or a date (2026-10-09)", f.since)
		}
		since = at
	}
	entries, err := feedback.List(since)
	if err != nil {
		return err
	}
	resp, err := feedbackResponse(entries, f.offset)
	if err != nil {
		return err
	}
	return f.render(w, resp)
}

// feedbackResponse builds the answer of --list from the reports left
// after skipping offset of them; an offset that skips them all is noted.
func feedbackResponse(entries []feedback.Entry, offset int) (*output.Response, error) {
	off := min(offset, len(entries))
	sec := output.Section{Title: "feedback", Offset: off, AllTags: true}
	for _, e := range entries[off:] {
		oe, err := feedbackEntry(e)
		if err != nil {
			return nil, err
		}
		sec.Entries = append(sec.Entries, oe)
	}
	resp := &output.Response{Bare: true, Sections: []output.Section{sec}}
	if offset > 0 && offset >= len(entries) {
		resp.Note = fmt.Sprintf("offset %d is past the end (total %d)", offset, len(entries))
	}
	return resp, nil
}

// feedbackEntry renders one report as a list entry: the time, where it
// was filed, the text on one line (its first feedbackLineText bytes),
// then what it is about and whether the index was behind the file. In
// JSON the entry carries the stored report whole as extra.
func feedbackEntry(e feedback.Entry) (output.Entry, error) {
	oe := output.Entry{Key: e.Time}
	if e.Workspace != "" {
		oe.Loc = strings.TrimSpace(strings.TrimSuffix(e.Workspace+"/"+e.Repo, "/") + " " + e.Head)
	}
	text := strings.Join(strings.Fields(e.Text), " ")
	if len(text) > feedbackLineText {
		text = strings.TrimSpace(strings.ToValidUTF8(text[:feedbackLineText], "")) // drops a character the cut went through
		oe.Tags = append(oe.Tags, fmt.Sprintf("text cut: %d bytes, whole with --format json --out FILE", len(e.Text)))
	}
	oe.Text = text
	for _, kv := range [][2]string{{"key", e.Key}, {"path", e.Path}, {"cmd", e.Cmd}} {
		if kv[1] != "" {
			oe.Tags = append(oe.Tags, kv[0]+":"+kv[1])
		}
	}
	if e.Dirty {
		oe.Tags = append(oe.Tags, "dirty")
	}
	if e.IndexedHash != e.CurrentHash {
		oe.Tags = append(oe.Tags, "index behind the file")
	}
	oe.Tags = append(oe.Tags, "codedoc "+e.Build+" extractor "+e.Extractor)
	raw, err := json.Marshal(e)
	if err != nil {
		return oe, err
	}
	return oe, json.Unmarshal(raw, &oe.Extra)
}

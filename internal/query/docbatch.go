package query

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/docs/write"
	"github.com/muralx/codedoc/internal/output"
)

// BatchMark starts the line that opens a record of a doc set batch:
// "=== <address>". A doc text line that starts with it cannot be written
// in a batch.
const BatchMark = "=== "

// ErrReported marks the error of a command that already printed its
// failures, one line each, on stderr: nothing more is printed for it.
var ErrReported = errors.New("the failures are listed on stderr")

// DocRecord is one record of a doc set batch: the address its "=== "
// line names and the doc text below it.
type DocRecord struct {
	Addr, Text string
}

// DocSetError is a doc that could not be set: what failed and what to
// do about it. Err is the cause, when there is one.
type DocSetError struct {
	Addr, What, Do string
	Err            error
}

// Error is the one line a failed codedoc doc set prints.
func (e *DocSetError) Error() string { return "doc set " + e.Line() }

// Line is the one line a batch prints for the failed record:
// "<address>: <what>; <what to do>".
func (e *DocSetError) Line() string { return e.Addr + ": " + e.What + "; " + e.Do }

// Unwrap returns the cause, so ErrNotFound and ErrAmbiguous keep their
// exit status.
func (e *DocSetError) Unwrap() error { return e.Err }

// ParseDocBatch splits the stdin of codedoc doc set --batch into its
// records: each starts with a line "=== <address>" and carries the lines
// up to the next such line or the end as its text, without the blank
// lines around it. failed has one line per part that is no record: text
// before the first "=== " line, a record without an address, a record
// without text.
func ParseDocBatch(src string) (recs []DocRecord, failed []string) {
	type raw struct {
		DocRecord
		line int
	}
	var raws []raw
	stray := false
	for i, line := range strings.Split(src, "\n") {
		line = strings.TrimSuffix(line, "\r")
		addr, opens := strings.CutPrefix(line, BatchMark)
		switch {
		case opens:
			raws = append(raws, raw{DocRecord{Addr: strings.TrimSpace(addr)}, i + 1})
		case len(raws) > 0:
			raws[len(raws)-1].Text += line + "\n"
		case strings.TrimSpace(line) != "" && !stray:
			stray = true
			failed = append(failed, fmt.Sprintf("line %d: text before the first \"=== <address>\" line; "+
				"start every record with one", i+1))
		}
	}
	for _, r := range raws {
		r.Text = trimBlankLines(r.Text)
		switch {
		case r.Addr == "":
			failed = append(failed, fmt.Sprintf("line %d: a record without an address; write \"=== <address>\"", r.line))
		case r.Text == "":
			failed = append(failed, fmt.Sprintf("%s: no doc text; write it on the lines after \"=== %s\"", r.Addr, r.Addr))
		default:
			recs = append(recs, r.DocRecord)
		}
	}
	return recs, failed
}

// trimBlankLines drops the blank lines a text starts and ends with.
func trimBlankLines(text string) string {
	lines := strings.Split(text, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// DocSetBatch sets the doc of every record it can, as DocSet does one by
// one, with one freshness check and one re-index per repo for the whole
// batch: the addresses are resolved against the index as it was before
// the first write, and the files written are re-indexed together at the
// end. failed has one line per record that was not written (see
// DocSetError.Line). The response lists the docs written; it is nil when
// there are none.
func DocSetBatch(c *Ctx, recs []DocRecord) (resp *output.Response, failed []string, err error) {
	var done []docWrite
	for _, rec := range recs {
		w, candidates, err := c.writeDoc(rec.Addr, rec.Text)
		if err != nil {
			failed = append(failed, explainDocSet(rec.Addr, candidates, err).Line())
			continue
		}
		done = append(done, w)
	}
	repos, err := c.reindexWritten(done)
	if err != nil || len(done) == 0 {
		return nil, failed, err
	}
	sec := output.Section{Title: "doc set"}
	for _, w := range done {
		e, err := c.docWritten(w)
		if err != nil {
			return nil, failed, err
		}
		sec.Entries = append(sec.Entries, e)
	}
	return c.respond(repos, sec), failed, nil
}

// reindexWritten re-indexes the files a batch wrote, each repo once, and
// returns those repos in config order.
func (c *Ctx) reindexWritten(done []docWrite) ([]*config.Repo, error) {
	paths := map[string][]string{}
	for _, w := range done {
		if !slices.Contains(paths[w.repo.Name], w.res.Path) {
			paths[w.repo.Name] = append(paths[w.repo.Name], w.res.Path)
		}
	}
	var repos []*config.Repo
	for _, r := range c.Workspace.Repos {
		if len(paths[r.Name]) == 0 {
			continue
		}
		if err := c.reindex(r, paths[r.Name]...); err != nil {
			return nil, err
		}
		repos = append(repos, r)
	}
	return repos, nil
}

// candidatesShown is how many candidates of an ambiguous address a
// failure line names.
const candidatesShown = 3

// explainDocSet turns the failure of writing the doc of addr into what
// failed and what to do; resp is the response that came with the error
// (the candidates of an ambiguous address).
func explainDocSet(addr string, resp *output.Response, err error) *DocSetError {
	var known *DocSetError
	if errors.As(err, &known) {
		return known
	}
	e := &DocSetError{Addr: addr, Err: err}
	msg := strings.TrimPrefix(strings.Join(strings.Fields(err.Error()), " "), "write: ")
	switch {
	case errors.Is(err, ErrNotFound):
		e.What, e.Do = "not found", "look its name up with codedoc find, or its file with codedoc outline"
	case errors.Is(err, ErrAmbiguous):
		keys := candidateKeys(resp)
		e.What, e.Do = fmt.Sprintf("ambiguous (%d candidates)", len(keys)), "address one by its key: "+shown(keys)
	case errors.Is(err, ErrDependency):
		e.What, e.Do = "is a dependency symbol", "doc set writes the sources of the workspace only"
	case errors.Is(err, write.ErrGenerated):
		e.What, e.Do = "is in a generated file", "document the source it is generated from"
	case strings.Contains(msg, "verification failed"):
		e.What, e.Do = msg, "the file was restored: fix the text and set it again"
	default:
		e.What, e.Do = msg, "nothing was written for it: fix that and set it again"
	}
	return e
}

// candidateKeys returns the keys of the candidates an ambiguous answer
// lists.
func candidateKeys(resp *output.Response) []string {
	var keys []string
	if resp == nil {
		return keys
	}
	for _, s := range resp.Sections {
		for _, e := range s.Entries {
			keys = append(keys, e.Key)
		}
	}
	return keys
}

// shown joins the first candidatesShown keys and counts the rest.
func shown(keys []string) string {
	if len(keys) <= candidatesShown {
		return strings.Join(keys, ", ")
	}
	return fmt.Sprintf("%s (and %d more: codedoc find lists them)", strings.Join(keys[:candidatesShown], ", "),
		len(keys)-candidatesShown)
}

// Package output renders query results as compact text or JSON under a byte budget.
package output

import (
	"fmt"
	"io"
	"strings"
)

// Header describes the index state the answer was computed from: one
// repo's head, or for an answer drawing on several repos (Workspace set)
// the head of each of them.
type Header struct {
	Repo, Branch, Commit string
	Dirty                int
	Fresh                bool
	// Workspace names the workspace of a workspace-wide answer; Repos
	// then lists the repos the answer draws on, in config order, and Main
	// names the one most of its entries come from. Stale names the repos
	// whose index is behind their working tree; a one-repo header names
	// them only when another repo than its own is among them.
	Workspace string
	Repos     []RepoHead
	Main      string
	Stale     []string
	// Multi tells that the workspace holds several repos: only then does
	// the text header of a one-repo answer name the repo and its commit.
	Multi bool
}

// RepoHead is the head of one repo of a workspace-wide answer.
type RepoHead struct {
	Name, Branch, Commit string
	Dirty                int
	Fresh                bool
}

// Entry is one ranked result line. Body, when set, is a multi-line block
// (printed with line numbers starting at BodyStart) that is dropped as a
// whole when it does not fit; its line range BodyStart-BodyEnd is kept.
// With Continue the body is cut to the lines that fit instead.
type Entry struct {
	Key string // stable key; text prints it only with Response.Keys or for want of a name
	// Name is the short qualified name text prints in place of the key
	// (AgentStore.register); empty prints the key.
	Name      string
	Loc       string   // path:line
	Text      string   // signature or one-line description
	Doc       string   // first sentence of the doc, may be empty
	Tags      []string // e.g. "ambiguous", "test"
	Body      string
	BodyStart int
	// BodyEnd is the last line of the body; zero means it is derived from
	// BodyStart and the number of body lines.
	BodyEnd int
	// Plain prints the body as it is, without line numbers (a diff).
	Plain bool
	// Continue is the command that prints a line range of the file the
	// body is read from, once "A-B" follows it (codedoc lines repo:path).
	// A body that does not fit is then cut to the lines that do and ends
	// with that command for the rest; only when not one line fits is it
	// dropped as a whole.
	Continue string
	// Depth nests the entry in a tree: text indents it two spaces per
	// level, JSON reports it.
	Depth int
	Extra map[string]any // JSON only
	// RepoID is the repo row of the path in Loc, zero when Loc names its
	// repo or no path; the query layer uses it to prefix Loc with the
	// repo name in answers drawing on several repos. It is not rendered.
	RepoID int64
	// Fragments are the lines of files that answer the question for this
	// entry (the call line, the lines a search matched), printed whole
	// under the entry line with their line numbers.
	Fragments []Fragment
	// Mark is printed before the name in text: "?" for an edge whose
	// target is one of several candidates. The ambiguous tag of a marked
	// entry is left out of text, the mark says it.
	Mark string
	// Site marks an entry whose fragments say more than its signature and
	// doc (a call site): text prints the fragments in their place, a
	// single line on the entry line itself, unless Response.Detail is
	// set; JSON keeps all of it.
	Site bool
	// Hide names what text leaves out of this entry line, besides what
	// its section hides.
	Hide Hide
	// Covers is the number of ranked entries the line stands for when it
	// is more than itself (the last caller listed of a file whose other
	// callers are left out); zero means one. Titles, omission lines and
	// offsets count it.
	Covers int
	// More is a line printed after the fragments of the entry: what it
	// leaves out and the command that prints it.
	More string
	// lead is the path line text prints before the first entry of a file
	// group (see textWriter.display).
	lead string
}

// Fragment is a run of real lines of a file: Text holds them, Start is
// the number of the first one. Path names the file when it is not the
// one in the Loc of the entry.
type Fragment struct {
	Path  string `json:"path,omitempty"`
	Start int    `json:"start"`
	End   int    `json:"end"`
	Text  string `json:"text"`
}

// Hide names the parts of an entry line a section leaves out of the text
// layout unless the response asks for detail; JSON keeps them.
type Hide uint8

// The parts of an entry line a section may hide.
const (
	HideText Hide = 1 << iota // the signature
	HideDoc                   // the doc sentence
)

// Section groups entries under a title; sections render in order.
type Section struct {
	Title string
	// KeyTitle is the title with stable keys in place of names: JSON
	// reports it and text prints it with Response.Keys. Empty means
	// Title.
	KeyTitle string
	Entries  []Entry
	// Offset is the number of ranked entries the caller already skipped;
	// the follow-up for omitted entries continues from Offset plus the
	// entries written.
	Offset int
	// Total is the number of ranked entries when the caller holds more
	// than it passed (a search passes one page of its hits): the title
	// counts them all and a line after the entries says how to get the
	// rest. Zero, or less than Offset plus the entries, means just those.
	Total int
	// Path is the file every entry of the section is expected in (the
	// file of an outline): text prints only the line range of an entry
	// located in it.
	Path string
	// NoTitle leaves the title line out of the text layout: the answer
	// is its one entry (a symbol, a line range).
	NoTitle bool
	// Hide names what text leaves out of the entry lines without
	// Response.Detail.
	Hide Hide
	// AllTags prints every tag in text; else only those that change what
	// the reader does (see essentialTag) unless Response.Detail is set.
	AllTags bool
	// Dirs tells that the entries are files without a line: text prints
	// the directory of a run of two or more of them once and their names
	// under it.
	Dirs bool
	// Command is the command that lists the entries of this section
	// alone (codedoc callers X in an answer of several questions): its
	// omission line names it in place of Response.Command.
	Command string
	// Room is the share of the budget the text of this section may take,
	// in bytes; what it leaves unused goes to the sections after it. A
	// section cut by its room does not cost the later ones their place.
	// Zero means the rest of the budget. JSON spends the budget in order.
	Room int
}

// covered is the number of ranked entries a run of entries stands for.
func covered(entries []Entry) int {
	n := 0
	for _, e := range entries {
		n += max(e.Covers, 1)
	}
	return n
}

// keyTitle is the title of the section with stable keys.
func (s Section) keyTitle() string {
	if s.KeyTitle != "" {
		return s.KeyTitle
	}
	return s.Title
}

// count is the number of ranked entries the title of the section
// reports.
func (s Section) count() int { return max(s.Total, s.Offset+covered(s.Entries)) }

// held is the number of ranked entries after those of the section that
// the caller did not pass.
func (s Section) held() int { return s.count() - s.Offset - covered(s.Entries) }

// Response is a complete answer.
type Response struct {
	Header   Header
	Sections []Section
	Note     string // one line printed last, e.g. stale hint
	// Bare marks an answer computed from no index (the list of feedback
	// reports): it is rendered without the header.
	Bare bool
	// Keys makes text print stable keys in place of names (--keys);
	// Detail every tag, signature and doc (--detail).
	Keys, Detail bool
	// Command is the command line that produced the answer, without
	// --offset: text builds from it the exact command that prints the
	// entries a budget left out.
	Command string
}

// CheckFormat reports an error unless format is "", "text" or "json".
func CheckFormat(format string) error {
	switch format {
	case "", "text", "json":
		return nil
	default:
		return fmt.Errorf("unknown format %q (want text or json)", format)
	}
}

// Render writes the response in the given format ("text", the default
// when empty, or "json"), keeping the output within budget bytes (0 means
// unlimited). The header is always written (unless the response is bare),
// so a budget smaller than the header yields just the header. It returns
// the number of omitted entries.
func Render(w io.Writer, r *Response, format string, budget int) (omitted int, err error) {
	if err := CheckFormat(format); err != nil {
		return 0, err
	}
	if format == "json" {
		return renderJSON(w, r, budget)
	}
	return renderText(w, r, budget)
}

// HeadRef renders name@commit (a branch or a repo with its head) with a
// seven-character commit and "-" for a blank name or commit.
func HeadRef(name, commit string) string {
	return dash(name) + "@" + dash(commit[:min(len(commit), 7)])
}

// HeaderLine is the text header, holding only what changes decisions:
// `repo@commit` when the workspace has several repos (up to three repos
// of a workspace-wide answer, or for more their count with the main one:
// `4 repos (go-lib@f8fd258 +3)`), `dirty` when a repo has uncommitted
// changes and `fresh:no`, naming the stale repos of a workspace-wide
// answer, when the index is behind. A fresh, clean answer of a workspace
// of one repo has no header: the line is empty.
func HeaderLine(h Header) string {
	var parts []string
	switch {
	case h.Workspace != "" && len(h.Repos) > 0 && (h.Multi || len(h.Repos) > 1):
		parts = append(parts, repoList(h.Repos, h.Main))
	case h.Workspace == "" && h.Multi:
		parts = append(parts, HeadRef(h.Repo, h.Commit))
	}
	if h.Dirty > 0 {
		parts = append(parts, "dirty")
	}
	if !h.Fresh {
		parts = append(parts, staleWord(h))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ") + "\n"
}

// staleWord says that the index is behind: fresh:no, naming the stale
// repos when they are not just the repo of the header.
func staleWord(h Header) string {
	if len(h.Stale) > 0 && (h.Workspace != "" || len(h.Stale) > 1 || h.Stale[0] != h.Repo) {
		return "fresh:no(" + strings.Join(h.Stale, ",") + ")"
	}
	return "fresh:no"
}

// maxHeaderRepos is the most repos a header names one by one.
const maxHeaderRepos = 3

// repoList renders the repos of a workspace header; with more than
// maxHeaderRepos it names main (else the first) and counts the rest.
func repoList(repos []RepoHead, main string) string {
	heads := make([]string, len(repos))
	first := 0
	for i, r := range repos {
		heads[i] = HeadRef(r.Name, r.Commit)
		if r.Name == main {
			first = i
		}
	}
	if len(repos) > maxHeaderRepos {
		return fmt.Sprintf("%d repos (%s +%d)", len(repos), heads[first], len(repos)-1)
	}
	return strings.Join(heads, " ")
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// bodyEnd is the last line of e's body.
func bodyEnd(e Entry) int {
	if e.BodyEnd != 0 {
		return e.BodyEnd
	}
	return e.BodyStart + strings.Count(strings.TrimSuffix(e.Body, "\n"), "\n")
}

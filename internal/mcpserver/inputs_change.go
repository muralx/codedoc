package mcpserver

import (
	"fmt"
	"strings"

	"github.com/muralx/codedoc/internal/query"
)

// HistoryInput is the input of codedoc_history: mode changes takes since,
// mode history takes symbol, mode at takes symbol, ref and diff.
type HistoryInput struct {
	Common
	Mode   string `json:"mode" jsonschema:"changes (what changed since a ref or duration), history (commits touching a symbol) or at (a symbol at a ref)"`
	Since  string `json:"since,omitempty" jsonschema:"changes: a ref (HEAD, main, a hash) or a duration (12h, 3d)"`
	Symbol string `json:"symbol,omitempty" jsonschema:"history and at: the symbol address"`
	Ref    string `json:"ref,omitempty" jsonschema:"at: the ref to show the symbol at"`
	Diff   string `json:"diff,omitempty" jsonschema:"at: diff the symbol between ref and this ref; true diffs against the working tree"`
}

func (in HistoryInput) build(a *argv) error {
	switch in.Mode {
	case "changes":
		a.words = []string{"changes"}
		a.str("since", in.Since)
		return required("since", in.Since, "for mode changes")
	case "history":
		a.words = []string{"history"}
		a.arg(in.Symbol)
		return required("symbol", in.Symbol, "for mode history")
	case "at":
		a.words = []string{"at"}
		a.str("ref", in.Ref)
		if in.Diff == "true" {
			a.flag("--diff")
		} else {
			a.str("diff", in.Diff)
		}
		a.arg(in.Symbol)
		if err := required("symbol", in.Symbol, "for mode at"); err != nil {
			return err
		}
		return required("ref", in.Ref, "for mode at")
	}
	return fmt.Errorf("mode %q: want changes, history or at", in.Mode)
}

// DocInput is the input of codedoc_doc: action gaps takes target (a
// module) and kind; set takes target and text, or docs for several docs
// in one call; check takes none.
type DocInput struct {
	Common
	Action string    `json:"action" jsonschema:"gaps (rank undocumented and stale symbols and modules), set (write a doc into the source; answers ok) or check (doc states per repo)"`
	Target string    `json:"target,omitempty" jsonschema:"set: the symbol, or module as repo:dir/ or identity; gaps: a module to limit the ranking to"`
	Text   string    `json:"text,omitempty" jsonschema:"set: the doc text without comment markers, summary sentence first"`
	Docs   []DocItem `json:"docs,omitempty" jsonschema:"set: several docs in one call, instead of target and text; the answer has one line per doc that could not be written"`
	Kind   string    `json:"kind,omitempty" jsonschema:"gaps: missing, stale or module"`
}

// writes reports whether the call sets docs.
func (in DocInput) writes() bool { return in.Action == "set" }

// DocItem is one doc of the docs input of codedoc_doc.
type DocItem struct {
	Address string `json:"address" jsonschema:"the symbol or module"`
	Text    string `json:"text" jsonschema:"the doc text"`
}

// buildBatch turns the docs input into codedoc doc set --batch and its
// stdin: a line "=== <address>" then the text, per doc. A text with a
// line that starts with "=== " would be read as another record, and an
// address cannot span lines, so both are refused.
func (in DocInput) buildBatch(a *argv) error {
	if in.Target != "" || in.Text != "" {
		return fmt.Errorf("inputs target and text do not go with docs: put every doc in docs")
	}
	a.words = []string{"doc", "set"}
	a.flag("--batch")
	var b strings.Builder
	for i, d := range in.Docs {
		addr := strings.TrimSpace(d.Address)
		if addr == "" || strings.ContainsAny(addr, "\r\n") {
			return fmt.Errorf("docs[%d]: address must be one non-empty line", i)
		}
		if strings.HasPrefix(d.Text, query.BatchMark) || strings.Contains(d.Text, "\n"+query.BatchMark) {
			return fmt.Errorf("docs[%d] (%s): a line of the text starts with %q, which docs cannot carry; "+
				"set that doc alone with target and text", i, addr, query.BatchMark)
		}
		b.WriteString(query.BatchMark + addr + "\n" + d.Text + "\n")
	}
	a.stdin = b.String()
	return nil
}

func (in DocInput) build(a *argv) error {
	switch in.Action {
	case "gaps":
		a.words = []string{"gaps"}
		a.str("kind", in.Kind)
		a.arg(in.Target)
		return nil
	case "set":
		if len(in.Docs) > 0 {
			return in.buildBatch(a)
		}
		a.words = []string{"doc", "set"}
		a.str("text", in.Text)
		a.arg(in.Target)
		if err := required("target", in.Target, "for action set"); err != nil {
			return err
		}
		return required("text", in.Text, "for action set")
	case "check":
		a.words = []string{"doc", "check"}
		return nil
	}
	return fmt.Errorf("action %q: want gaps, set or check", in.Action)
}

// DepsInput is the input of codedoc_deps: action list (the default)
// lists the packages the workspace depends on, index discovers and
// indexes them now, prune deletes the package versions no workspace
// uses (codedoc deps prune has --all and --dry-run).
type DepsInput struct {
	Common
	Action string `json:"action,omitempty" jsonschema:"list (the default: packages with their states), index (discover and index now) or prune (delete the package versions no workspace uses)"`
}

// writes reports whether the call indexes or deletes package versions.
func (in DepsInput) writes() bool { return in.Action == "index" || in.Action == "prune" }

func (in DepsInput) build(a *argv) error {
	switch in.Action {
	case "", "list":
		a.words = []string{"deps"}
	case "index":
		a.words = []string{"deps", "index"}
	case "prune":
		a.words = []string{"deps", "prune"}
		if in.Workspace != "" || in.Repo != "" {
			return fmt.Errorf("inputs workspace and repo do not apply to action prune: it covers every workspace of the config")
		}
	default:
		return fmt.Errorf("action %q: want list, index or prune", in.Action)
	}
	return nil
}

// FeedbackInput is the input of codedoc_feedback.
type FeedbackInput struct {
	Common
	Text string `json:"text,omitempty" jsonschema:"what was asked, what came back and what the file says; required unless list is set"`
	Path string `json:"path,omitempty" jsonschema:"file the answer was about"`
	List bool   `json:"list,omitempty" jsonschema:"print the reports filed instead of filing one"`
}

// writes reports whether the call files a report.
func (in FeedbackInput) writes() bool { return !in.List }

func (in FeedbackInput) build(a *argv) error {
	a.words = []string{"feedback"}
	a.str("path", in.Path)
	a.boolean("list", in.List)
	a.arg(in.Text)
	if in.List {
		return nil
	}
	return required("text", in.Text, "unless list is set")
}

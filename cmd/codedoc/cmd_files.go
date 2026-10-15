package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/muralx/codedoc/internal/buildinfo"
	"github.com/muralx/codedoc/internal/feedback"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/query"
	"github.com/muralx/codedoc/internal/store"
)

func newLsCmd() *cobra.Command {
	var q queryFlags
	var o query.LsOpts
	cmd := &cobra.Command{
		Use:   "ls [path|glob ...]",
		Short: "List indexed files by date, size or name; --long adds size, kind, symbol count and title",
		Example: "  codedoc ls --since 2d\n  codedoc ls docs --kind doc --sort name\n" +
			"  codedoc ls 'src/**/*.ts' --sort lines\n  codedoc ls --dirs --depth 2",
		Long: "List indexed files, newest first: what ls -lt, find -newer and tree are used for.\n\n" +
			"Each line is the path, the modification time and the line count, the files of a directory under its " +
			"name; --long adds the date of the last commit when it differs, size, kind/language, symbol count and " +
			"title. Without an argument it lists the working directory recursively. Files that are not indexed are " +
			"counted by reason, never listed.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return q.run(cmd, func(c *query.Ctx) (*output.Response, error) { return query.Ls(c, args, o) })
		},
	}
	q.register(cmd)
	cmd.Flags().StringVar(&o.Sort, "sort", "mtime", "sort by "+strings.Join(query.LsSorts, ", ")+" (newest or largest first)")
	cmd.Flags().BoolVar(&o.Reverse, "reverse", false, "invert the order")
	cmd.Flags().StringVar(&o.Since, "since", "", "modified at or after: a duration (2d, 3h), a date or a git ref")
	cmd.Flags().StringVar(&o.Until, "until", "", "modified at or before: a duration, a date or a git ref")
	cmd.Flags().StringArrayVar(&o.Kinds, "kind", nil, "file kind: "+strings.Join(query.LsKinds, ", ")+" (repeatable)")
	cmd.Flags().StringVar(&o.Lang, "lang", "", "language, family (go, ts, doc) or config format")
	cmd.Flags().IntVar(&o.Depth, "depth", 0, "limit the directory depth below the path argument (0 means unlimited)")
	cmd.Flags().BoolVar(&o.Dirs, "dirs", false, "one line per directory: file count, newest mtime, total lines and size")
	cmd.Flags().BoolVarP(&o.Long, "long", "l", false, "add size, kind, symbol count, title and commit date to each file")
	return cmd
}

func newLinesCmd() *cobra.Command {
	var q queryFlags
	cmd := &cobra.Command{
		Use:   "lines <path> <A-B|A+N|A>...",
		Short: "Print line ranges of a file, each under the symbol that encloses it",
		Example: "  codedoc lines store/store.go 20-40\n  codedoc lines go-lib:store/store.go 26+6\n" +
			"  codedoc lines store/store.go 10-20 40-55",
		Long: "Print lines A to B of a file on disk (A+N: A and the N lines after it), with line numbers; several " +
			"ranges are printed in line order, those that overlap or touch as one.\n\n" +
			"The header of a range names the innermost symbol or section that encloses all of it, or the ones it " +
			"starts and ends in. A range larger than the budget ends with the command for the rest. It is also the " +
			"cross-check of the index: a symbol whose recorded range does not start at its declaration is reported " +
			"as an index mismatch. To read a symbol, use codedoc symbol.",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return q.run(cmd, func(c *query.Ctx) (*output.Response, error) { return query.Lines(c, args[0], args[1:]...) })
		},
	}
	q.register(cmd)
	// The lines asked for are the answer: the budget only keeps a range
	// far larger than a reader wants from flooding the session.
	budget := cmd.Flags().Lookup("budget")
	budget.DefValue, q.budget = strconv.Itoa(linesBudget), linesBudget
	return cmd
}

// linesBudget is the default output budget of codedoc lines in bytes:
// what a shell tool returns of one command before it cuts it.
const linesBudget = 30000

// feedbackFlags are the flags of codedoc feedback.
type feedbackFlags struct {
	// queryFlags are the scope of a report, and for --list the budget,
	// the offset and the output file of the answer.
	queryFlags
	// writeFlags is --verbose: filing a report and --clear print
	// nothing without it.
	writeFlags
	o           query.FeedbackOpts
	list, clear bool
	since       string
}

func newFeedbackCmd() *cobra.Command {
	var f feedbackFlags
	cmd := &cobra.Command{
		Use:   "feedback <what was asked, what came back, what the file says>",
		Short: "Report an answer that disagrees with the file; stays on this machine",
		Example: "  codedoc feedback \"symbol --body of MemStore.Get ends one line early\" --key go-lib:go:...:MemStore.Get\n" +
			"  codedoc feedback --list --since 7d\n  codedoc feedback --clear",
		Long: "Append a report to the feedback file under the state directory ($XDG_STATE_HOME/codedoc/feedback.jsonl): " +
			"the text, the workspace, repo and head, the codedoc build and extractor version, and for --key or --path " +
			"the hash the index holds for the file and the hash of the file on disk. Nothing leaves the machine.\n\n" +
			"The file is bounded: a text is cut to 8 KB, every other field to 1 KB, and a file of 4 MB drops its oldest " +
			"reports. --list is cut by --budget and continued with --offset like a query answer.\n\n" +
			"Filing a report and --clear print nothing when they succeed (the exit status is the result); --verbose " +
			"prints where the report went, or how many were cleared.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := output.CheckFormat(f.sc.format); err != nil {
				return err
			}
			switch {
			case f.clear:
				return clearFeedback(f.dest(cmd, f.sc.format))
			case f.list:
				return listFeedback(cmd.OutOrStdout(), f)
			case len(args) == 0 || strings.TrimSpace(args[0]) == "":
				return usage("feedback needs a text, or --list or --clear")
			}
			return recordFeedback(f.dest(cmd, f.sc.format), f, args[0])
		},
	}
	f.queryFlags.register(cmd)
	f.writeFlags.register(cmd)
	cmd.Flags().StringVar(&f.o.Key, "key", "", "stable key of the symbol the answer was about")
	cmd.Flags().StringVar(&f.o.Path, "path", "", "file the answer was about")
	cmd.Flags().StringVar(&f.o.Cmd, "cmd", "", "the command that gave the answer")
	cmd.Flags().BoolVar(&f.list, "list", false, "print the reports filed")
	cmd.Flags().StringVar(&f.since, "since", "", "with --list: only reports filed since a duration (7d, 3h) or a date")
	cmd.Flags().BoolVar(&f.clear, "clear", false, "empty the feedback file")
	cmd.MarkFlagsMutuallyExclusive("list", "clear")
	return cmd
}

// recordFeedback files one report. Inside a workspace it describes the
// index the answer came from (see query.FeedbackEntry); outside one,
// unless --workspace or --repo asked for one, it records the text with
// the build alone.
func recordFeedback(w io.Writer, f feedbackFlags, text string) error {
	e := feedback.Entry{Time: time.Now().UTC().Format(time.RFC3339), Build: buildinfo.Version,
		Extractor: index.ExtractorVersion(), Text: text, Key: f.o.Key, Path: f.o.Path, Cmd: f.o.Cmd}
	t, err := f.sc.resolve()
	switch {
	case err == nil:
		s, err := store.Open(t.cfg.DBPath(t.ws.Name))
		if err != nil {
			return err
		}
		defer func() { _ = s.Close() }()
		c := &query.Ctx{Store: s, Workspace: t.ws, Repo: cwdRepo(t)}
		if f.sc.repo != "" {
			c.Repo = t.repos[0]
		}
		c.Cwd, _ = os.Getwd()
		e = query.FeedbackEntry(c, text, f.o)
	case f.sc.workspace != "" || f.sc.repo != "":
		return err
	}
	if err := feedback.Append(e); err != nil {
		return err
	}
	p, err := feedback.File()
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "feedback recorded in %s\n", p)
	return err
}

// clearFeedback empties the feedback file.
func clearFeedback(w io.Writer) error {
	n, err := feedback.Clear()
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "feedback cleared (%d %s)\n", n, map[bool]string{true: "report", false: "reports"}[n == 1])
	return err
}

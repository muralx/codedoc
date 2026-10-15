package main

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/query"
)

func newDocCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doc",
		Short: "Write and check documentation",
	}
	cmd.AddCommand(newDocSetCmd(), newDocCheckCmd())
	return cmd
}

// docSetLong is the help of codedoc doc set.
const docSetLong = "Write the doc of a symbol or module into the source, in the language's doc form, verify it and " +
	"reindex the file. It prints nothing when the doc is written: the exit status is the result. A doc that " +
	"cannot be written is one line on stderr saying what failed and what to do. --verbose prints the new " +
	"doc span and state.\n\nThe text comes from --text or stdin, " +
	"without comment markers. Modules are addressed by identity or repo:dir/ (trailing slash). " +
	"Setting a doc marks it ok even when the text is unchanged.\n\n" +
	"--batch writes several docs in one call. Stdin carries one record per doc: a line \"=== <address>\", " +
	"then the doc text up to the next \"=== \" line or the end (blank lines around the text are dropped). " +
	"Every record that can be written is written; each one that cannot is one line on stderr, " +
	"\"<address>: <what>; <what to do>\", and the exit status is 1. A doc with a line that starts with " +
	"\"=== \" cannot be written with --batch: set it alone."

func newDocSetCmd() *cobra.Command {
	var q queryFlags
	var w writeFlags
	var text string
	var batch bool
	cmd := &cobra.Command{
		Use:   "set <symbol|module>",
		Short: "Write the doc of a symbol or module into the source and reindex the file",
		Long:  docSetLong,
		Example: "  codedoc doc set store/store.go#MemStore.Get --text \"Get returns the value stored under key.\"\n" +
			"  codedoc doc set go-lib:store/ < purpose.txt\n" +
			"  codedoc doc set --batch <<'DOCS'\n  === store/store.go#MemStore.Get\n  Get returns the value stored under key.\n" +
			"  === go-lib:store/\n  Package store keeps values by key.\n  DOCS",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			q.stdout = w.dest(cmd, q.sc.format)
			if batch {
				return runDocBatch(cmd, &q, args)
			}
			if len(args) == 0 {
				return usage("doc set: no address (give a symbol or module, or --batch with records on stdin)")
			}
			if !cmd.Flags().Changed("text") {
				b, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return err
				}
				text = string(b)
			}
			if text == "" {
				return usage("doc set: no text (give --text or pipe it on stdin)")
			}
			return q.run(cmd, func(c *query.Ctx) (*output.Response, error) { return query.DocSet(c, args[0], text) })
		},
	}
	q.register(cmd)
	w.register(cmd)
	cmd.Flags().StringVar(&text, "text", "", "the doc text (default: read stdin)")
	cmd.Flags().BoolVar(&batch, "batch", false, "read records from stdin, each a line \"=== <address>\" then the doc text")
	cmd.MarkFlagsMutuallyExclusive("batch", "text")
	return cmd
}

// runDocBatch is codedoc doc set --batch: it writes every record of
// stdin it can (see query.DocSetBatch) and reports the others, one line
// each (see reportFailures).
func runDocBatch(cmd *cobra.Command, q *queryFlags, args []string) error {
	if len(args) > 0 {
		return usage("doc set --batch: the addresses come from stdin (\"=== <address>\" lines), not from an argument")
	}
	src, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return err
	}
	recs, failed := query.ParseDocBatch(string(src))
	total := len(recs) + len(failed)
	if total == 0 {
		return usage("doc set --batch: no records on stdin; write a line \"=== <address>\" and the doc text below it, once per doc")
	}
	if len(recs) > 0 {
		err = q.run(cmd, func(c *query.Ctx) (*output.Response, error) {
			resp, more, err := query.DocSetBatch(c, recs)
			failed = append(failed, more...)
			return resp, err
		})
	}
	return reportFailures(cmd.ErrOrStderr(), failed, total, "docs", err)
}

func newDocCheckCmd() *cobra.Command {
	var q queryFlags
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Report doc states per repo and docs placed or formed wrongly",
		Long: "Re-extract every added, changed and deleted file of the workspace or --repo repo (no file limit, " +
			"like codedoc index), then report per repo the ok/stale/missing counts of symbols and modules, plus problems: " +
			"Go docs not starting with the symbol name (warning), Javadoc detached from its declaration by a blank " +
			"line, Python docstrings in single quotes. It always exits 0.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return q.run(cmd, query.DocCheck)
		},
	}
	q.register(cmd)
	return cmd
}

func newGapsCmd() *cobra.Command {
	var q queryFlags
	var o query.GapsOpts
	cmd := &cobra.Command{
		Use:   "gaps [module]",
		Short: "Rank undocumented and stale symbols and modules",
		Long: "Rank documentation gaps of the workspace, the --repo repo, or one module: stale before missing, then by " +
			"score = visibility x (1 + ln(1 + fan-in)) x kind weight x recency. Tests, generated files, trivial " +
			"accessors, short non-public symbols and methods of documented interfaces are left out.",
		Example: "  codedoc gaps --limit 20\n  codedoc gaps --kind stale --repo go-lib\n  codedoc gaps github.com/acme/auth/services",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			scope := ""
			if len(args) == 1 {
				scope = args[0]
			}
			return q.run(cmd, func(c *query.Ctx) (*output.Response, error) { return query.Gaps(c, scope, o) })
		},
	}
	q.register(cmd)
	cmd.Flags().StringVar(&o.Kind, "kind", "", "missing, stale or module")
	cmd.Flags().IntVar(&o.Limit, "limit", 0, "keep the first N gaps (0: all, within the budget)")
	return cmd
}

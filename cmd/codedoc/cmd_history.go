package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/query"
)

func newChangesCmd() *cobra.Command {
	var q queryFlags
	var since string
	cmd := &cobra.Command{
		Use:   "changes --since <ref|duration>",
		Short: "List files and symbols changed since a ref or duration, with their doc state",
		Long: "List the files that differ between a ref (or the newest commit older than a duration: 12h, 3d, 2w) " +
			"and the working tree, uncommitted and untracked files included, renames detected, and the symbols in " +
			"them tagged added, removed, moved (to another file of the changeset), body-changed, doc-changed, " +
			"doc-added or doc-removed, with their current doc state.",
		Example: "  codedoc changes --since HEAD\n  codedoc changes --since main --repo go-lib\n  codedoc changes --since 3d",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return q.run(cmd, func(c *query.Ctx) (*output.Response, error) { return query.Changes(c, since) })
		},
	}
	q.register(cmd)
	cmd.Flags().StringVar(&since, "since", "", "a ref (HEAD, main, a hash) or a duration (12h, 3d, 2w)")
	_ = cmd.MarkFlagRequired("since")
	return cmd
}

func newHistoryCmd() *cobra.Command {
	var q queryFlags
	var limit int
	cmd := &cobra.Command{
		Use:   "history <symbol>",
		Short: "List the commits that touched a symbol's lines",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return q.run(cmd, func(c *query.Ctx) (*output.Response, error) { return query.History(c, args[0], limit) })
		},
	}
	q.register(cmd)
	cmd.Flags().IntVar(&limit, "limit", 0, "at most N commits (0: all)")
	return cmd
}

func newAtCmd() *cobra.Command {
	var q queryFlags
	var o query.AtOpts
	cmd := &cobra.Command{
		Use:   "at <symbol> --ref A [--diff[=B]]",
		Short: "Show a symbol as it was at a ref, or diff it between two refs",
		Long: "Show a symbol as it was at ref A (found by qualified name in its current file at A), with line " +
			"numbers. With --diff, print a unified diff of just that symbol between A and B; B defaults to the " +
			"working tree. Give B with an equals sign: --diff=HEAD.",
		Example: "  codedoc at MemStore.Get --ref HEAD~3\n  codedoc at MemStore.Get --ref v1.2 --diff\n" +
			"  codedoc at MemStore.Get --ref v1.2 --diff=HEAD",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 2 && o.Diff == query.WorkTree {
				return fmt.Errorf("--diff takes its ref with an equals sign: --diff=%s", args[1])
			}
			return cobra.ExactArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return q.run(cmd, func(c *query.Ctx) (*output.Response, error) { return query.At(c, args[0], o) })
		},
	}
	q.register(cmd)
	cmd.Flags().StringVar(&o.Ref, "ref", "", "the ref to show the symbol at")
	cmd.Flags().StringVar(&o.Diff, "diff", "", "diff against this ref, as --diff=B (no value: the working tree)")
	cmd.Flags().Lookup("diff").NoOptDefVal = query.WorkTree
	_ = cmd.MarkFlagRequired("ref")
	return cmd
}

func newOverviewCmd() *cobra.Command {
	var q queryFlags
	cmd := &cobra.Command{
		Use:   "overview",
		Short: "Orient in the workspace: repos, headings, entry points, doc coverage, commits, modules",
		Long: "Per repo: branch, commit, dirty count and size; README.md and CLAUDE.md headings; entry points (main " +
			"functions, package main directories, cmd/* and bin/*, main/index/app/server files, server starts); doc " +
			"coverage; the last 10 commits; and the module tree to depth 2 with one-line docs, file and line counts (cut first by the budget).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return q.run(cmd, query.Overview) },
	}
	q.register(cmd)
	return cmd
}

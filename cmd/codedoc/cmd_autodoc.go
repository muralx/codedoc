package main

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/muralx/codedoc/internal/autodoc"
	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/daemon"
	"github.com/muralx/codedoc/internal/embed"
	"github.com/muralx/codedoc/internal/query"
	"github.com/muralx/codedoc/internal/store"
)

// autodocFlags are the flags of codedoc autodoc (spec 11.2).
type autodocFlags struct {
	sc         scope
	maxItems   int
	maxMinutes int
	commit     bool
	allowDirty bool
}

func newAutodocCmd() *cobra.Command {
	var f autodocFlags
	cmd := &cobra.Command{
		Use:   "autodoc --repo R",
		Short: "Document a repo's top gaps unattended through headless claude -p runs",
		Long: "Take the repo's top documentation gaps, grouped by module, and run headless `claude -p` per group; " +
			"the agent reads through codedoc commands and writes only with `codedoc doc set` (claude runs with only Bash " +
			"for codedoc commands and Read allowed, in permission mode dontAsk). A group is at most autodoc.group_size " +
			"keys (default 5) and may spend autodoc.max_cost_usd (default 1.00). A group whose keys are not all ok " +
			"afterwards, or whose agent changed anything else, is restored from the run journal. Refuses a dirty working tree unless " +
			"--allow-dirty, which disables --commit. A manual run ignores the session lock. Set CODEDOC_CLAUDE_BIN " +
			"to use another claude binary.",
		Example: "  codedoc autodoc --repo auth --max-items 20 --max-minutes 30 --commit",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if f.sc.repo == "" {
				return usage("autodoc needs --repo")
			}
			if f.maxItems <= 0 {
				return errors.New("--max-items must be a positive number")
			}
			return f.run(cmd)
		},
	}
	cmd.Flags().StringVar(&f.sc.workspace, "workspace", "", "workspace name (default: the one containing the current directory)")
	cmd.Flags().StringVar(&f.sc.repo, "repo", "", "the repo to document (required)")
	cmd.Flags().IntVar(&f.maxItems, "max-items", config.DefaultAutodocMaxItems, "most gaps to document")
	cmd.Flags().IntVar(&f.maxMinutes, "max-minutes", 30, "wall time limit of the run")
	cmd.Flags().BoolVar(&f.commit, "commit", false, "commit each documented module's files")
	cmd.Flags().BoolVar(&f.allowDirty, "allow-dirty", false, "run on a dirty working tree (disables --commit)")
	return cmd
}

// run documents the repo and prints the summary.
func (f *autodocFlags) run(cmd *cobra.Command) error {
	f.sc.format = "text"
	t, err := f.sc.resolve()
	if err != nil {
		return err
	}
	s, err := store.Open(t.cfg.DBPath(t.ws.Name))
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	c := &query.Ctx{Store: s, Workspace: t.ws, Embedder: embed.Configured(t.cfg.Embed.ModelDir, cmd.ErrOrStderr())}
	if c.Embedder != nil {
		defer func() { _ = c.Embedder.Close() }()
	}
	log, err := daemon.NewLogger(cmd.ErrOrStderr(), "")
	if err != nil {
		return err
	}
	r := t.repos[0]
	opts := autodoc.Options{Repo: r.Name, Root: r.Path, MaxItems: f.maxItems, MaxMinutes: f.maxMinutes,
		Commit: f.commit, AllowDirty: f.allowDirty, GroupSize: t.ws.Autodoc.GroupSize, MaxCostUSD: t.ws.Autodoc.MaxCostUSD,
		Model: t.ws.Autodoc.Model}
	// SIGINT or SIGTERM cancels the run: the group in progress has its
	// claude process group killed and its files restored.
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	sum, err := autodoc.Run(ctx, opts, autodoc.Indexed{Ctx: c}, autodoc.Indexed{Ctx: c}, autodoc.Git{}, log)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), sum)
	return err
}

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/scip"
	"github.com/muralx/codedoc/internal/store"
)

func newSCIPCmd() *cobra.Command {
	root := &cobra.Command{Use: "scip", Short: "Precise edges from SCIP indexers"}
	var sc scope
	run := &cobra.Command{
		Use:   "run",
		Short: "Index the repos, run the SCIP indexers of their languages and merge precise edges",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			t, err := sc.resolve()
			if err != nil {
				return err
			}
			resp, failed, err := runSCIP(cmd.Context(), t, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			if _, err := output.Render(cmd.OutOrStdout(), resp, sc.format, 0); err != nil {
				return err
			}
			if failed > 0 {
				return fmt.Errorf("%d SCIP indexer run(s) failed", failed)
			}
			return nil
		},
	}
	sc.register(run)
	root.AddCommand(run)
	return root
}

// runSCIP brings the target repos' index up to date (the merge matches
// occurrences to stored symbols and references by position), then runs
// the SCIP indexer of each language family the repos' files use and
// merges their indexes, every repo's definitions before any repo's
// references (see scip.Runner.RunRepos). A missing indexer is reported
// once to warn and is not an error; failed runs are counted.
func runSCIP(ctx context.Context, t target, warn io.Writer) (*output.Response, int, error) {
	resp, unindexed, err := runIndex(t, false, warn)
	if err != nil {
		return nil, 0, err
	}
	if len(unindexed) > 0 { // the merge needs the index of every target repo
		return nil, 0, errors.New("index " + strings.Join(unindexed, "; index "))
	}
	s, err := store.Open(t.cfg.DBPath(t.ws.Name))
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = s.Close() }()
	rows := make([]store.Repo, len(t.repos))
	for i, repo := range t.repos {
		if rows[i], err = s.RepoByPath(repo.Path); err != nil {
			return nil, 0, fmt.Errorf("scip %s: %w", repo.Name, err)
		}
	}
	outcomes, err := scip.Runner{}.RunRepos(ctx, s, rows)
	if err != nil {
		return nil, 0, fmt.Errorf("scip: %w", err)
	}
	sec, failed := scipSection(t, outcomes, warn)
	resp.Sections = []output.Section{sec}
	return resp, failed, nil
}

// scipSection lists the outcomes of each target repo, warning once per
// missing indexer, and counts the failed runs.
func scipSection(t target, outcomes [][]scip.Outcome, warn io.Writer) (output.Section, int) {
	warned := map[string]bool{}
	failed := 0
	sec := output.Section{Title: "scip"}
	for i, repo := range t.repos {
		if len(outcomes[i]) == 0 {
			sec.Entries = append(sec.Entries, output.Entry{Key: repo.Name, Text: "no language with a SCIP indexer"})
		}
		for _, o := range outcomes[i] {
			if o.Missing != nil && !warned[o.Missing.Bin] {
				warned[o.Missing.Bin] = true
				_, _ = fmt.Fprintf(warn, "warning: %v; %s files keep heuristic edges\n", o.Missing, o.Lang)
			}
			if o.Failed {
				failed++
			}
			sec.Entries = append(sec.Entries, output.Entry{Key: repo.Name, Text: o.Lang + ": " + o.Text})
		}
	}
	return sec, failed
}

// scipStatus describes a repo's SCIP state for status: scip:failed(date)
// after a run with a failed indexer, until a run succeeds; else the
// commit its index was merged from (scip:never without one), with the
// indexers the last run found missing (scip:missing scip-go when none
// ever merged).
func scipStatus(s *store.Store, row store.Repo) (string, error) {
	rec, ok, err := scip.LastRun(s, row.Path)
	switch {
	case err != nil || !ok:
		return scipRef(row.SCIPCommit), err
	case rec.Failed:
		return "scip:failed(" + rec.At.Format(time.DateOnly) + ")", nil
	case len(rec.Missing) > 0 && row.SCIPCommit == "":
		return "scip:missing " + strings.Join(rec.Missing, ","), nil
	case len(rec.Missing) > 0:
		return scipRef(row.SCIPCommit) + " (missing " + strings.Join(rec.Missing, ",") + ")", nil
	}
	return scipRef(row.SCIPCommit), nil
}

// scipRef describes the commit a repo's SCIP index was merged from.
func scipRef(commit string) string {
	if commit == "" {
		return "scip:never"
	}
	return "scip:" + commit[:min(7, len(commit))]
}

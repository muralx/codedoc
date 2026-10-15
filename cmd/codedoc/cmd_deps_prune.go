package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/deps"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/store"
)

// pruneFlags are the switches of codedoc deps prune.
type pruneFlags struct {
	all, dryRun bool
}

// dest returns where a prune prints its list: a dry run is a question
// and always prints it; a prune that deletes is a write (see
// writeFlags.dest).
func (f pruneFlags) dest(cmd *cobra.Command, w *writeFlags, format string) io.Writer {
	if f.dryRun {
		return cmd.OutOrStdout()
	}
	return w.dest(cmd, format)
}

// runDepsPrune deletes from deps.db the package versions no workspace
// uses: neither a workspace of the config nor a workspace database a
// sync recorded in deps.db as a user (deps spec section 9), so a run
// under another config does not take the packages of the workspaces it
// cannot see. With all every version goes, and the indexed uses of the
// config's workspaces are marked pending so the next index run brings
// their packages back (any other workspace notices on its next query).
// With dryRun nothing is deleted. It lists what it deleted, or would.
func runDepsPrune(cfg *config.Config, f pruneFlags) (*output.Response, error) {
	resp := &output.Response{Header: output.Header{Workspace: config.DepsWorkspace, Fresh: true}}
	sec := output.Section{Title: "pruned"}
	if f.dryRun {
		sec.Title = "would prune"
	}
	resp.Sections = []output.Section{sec}
	if _, err := os.Stat(cfg.DepsDBPath()); errors.Is(err, fs.ErrNotExist) {
		return resp, nil
	}
	db, err := deps.Open(cfg.DepsDBPath())
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	pruned, err := db.Prune(deps.PruneOptions{All: f.all, DryRun: f.dryRun, Keep: func() (map[string]bool, error) {
		return configUses(cfg)
	}})
	if err == nil && f.all && !f.dryRun {
		err = eachWorkspaceStore(cfg, deps.ResetStates)
	}
	for _, id := range pruned.Packages {
		resp.Sections[0].Entries = append(resp.Sections[0].Entries, output.Entry{Key: id})
	}
	resp.Note = pruneNote(pruned)
	return resp, err
}

// configUses returns the package ids the workspaces of the config use.
func configUses(cfg *config.Config) (map[string]bool, error) {
	keep := map[string]bool{}
	err := eachWorkspaceStore(cfg, func(s *store.Store) error {
		ids, err := s.UsedDepPackageIDs()
		for _, id := range ids {
			keep[id] = true
		}
		return err
	})
	return keep, err
}

// pruneNote says whose packages a prune kept besides those of the
// config, and which recorded users are gone.
func pruneNote(p deps.Pruned) string {
	var parts []string
	if len(p.Users) > 0 {
		parts = append(parts, fmt.Sprintf("kept the packages of %d workspace database(s): %s", len(p.Users),
			strings.Join(p.Users, ", ")))
	}
	if len(p.Gone) > 0 {
		parts = append(parts, fmt.Sprintf("forgot %d deleted workspace database(s): %s", len(p.Gone),
			strings.Join(p.Gone, ", ")))
	}
	return strings.Join(parts, "; ")
}

// eachWorkspaceStore runs fn on the database of every workspace of the
// config that has one.
func eachWorkspaceStore(cfg *config.Config, fn func(*store.Store) error) error {
	for _, name := range cfg.Names() {
		if _, err := os.Stat(cfg.DBPath(name)); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		s, err := store.Open(cfg.DBPath(name))
		if err != nil {
			return err
		}
		err = errors.Join(fn(s), s.Close())
		if err != nil {
			return err
		}
	}
	return nil
}

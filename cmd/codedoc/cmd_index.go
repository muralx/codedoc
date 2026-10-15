package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/deps"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/store"
)

// scope holds the flags that select a workspace, a repo and the format.
type scope struct {
	workspace, repo, format string
}

func (sc *scope) register(cmd *cobra.Command) {
	cmd.Flags().StringVar(&sc.workspace, "workspace", "", "workspace name (default: the one containing the current directory)")
	cmd.Flags().StringVar(&sc.repo, "repo", "", "limit to one repo of the workspace")
	cmd.Flags().StringVar(&sc.format, "format", "text", "output format: text or json")
}

// target is a resolved scope: the config, a workspace and the selected repos.
type target struct {
	cfg   *config.Config
	ws    *config.Workspace
	repos []*config.Repo
}

// resolve validates the format, then loads the config and selects the
// workspace and repos.
func (sc *scope) resolve() (target, error) {
	if err := output.CheckFormat(sc.format); err != nil {
		return target{}, err
	}
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return target{}, err
	}
	ws, err := pickWorkspace(cfg, sc.workspace)
	if err != nil {
		return target{}, err
	}
	t := target{cfg: cfg, ws: ws, repos: ws.Repos}
	if sc.repo == "" {
		return t, nil
	}
	for _, r := range ws.Repos {
		if r.Name == sc.repo {
			t.repos = []*config.Repo{r}
			return t, nil
		}
	}
	return target{}, fmt.Errorf("workspace %s has no repo named %q", ws.Name, sc.repo)
}

func pickWorkspace(cfg *config.Config, name string) (*config.Workspace, error) {
	if name != "" {
		if ws, ok := cfg.Workspaces[name]; ok {
			return ws, nil
		}
		return nil, fmt.Errorf("unknown workspace %q%s", name, configuredHint(cfg))
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	ws, err := cfg.WorkspaceFor(cwd)
	if errors.Is(err, config.ErrOutsideRepos) && !strings.Contains(err.Error(), "use --workspace") {
		return nil, fmt.Errorf("%w; use --workspace NAME%s", err, configuredHint(cfg))
	}
	return ws, err
}

// configuredHint names the configured workspaces: " (configured: a, b)",
// or "" when there are none.
func configuredHint(cfg *config.Config) string {
	if len(cfg.Names()) == 0 {
		return ""
	}
	return " (configured: " + strings.Join(cfg.Names(), ", ") + ")"
}

func newIndexCmd() *cobra.Command {
	var sc scope
	var w writeFlags
	var rebuild bool
	cmd := &cobra.Command{
		Use:   "index",
		Short: "Index the repos of a workspace (incremental unless --rebuild)",
		Long: "Index the repos of a workspace, or the --repo one, and the package versions they depend on " +
			"(incremental unless --rebuild).\n\nIt prints nothing when it succeeds: the exit status is the result. " +
			"--verbose prints a summary per repo. A repo that cannot be indexed is one line on stderr, " +
			"\"<repo>: <what>; <what to do>\"; the other repos are still indexed and the exit status is 1. " +
			"A warning (an identity several repos provide, an embedding model that cannot load, package versions " +
			"that failed) is one line on stderr too.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			t, err := sc.resolve()
			if err != nil {
				return err
			}
			resp, failed, err := runIndex(t, rebuild, cmd.ErrOrStderr())
			if err == nil && resp != nil {
				if !w.shows(sc.format) {
					err = warnDeps(cmd.ErrOrStderr(), resp.Note)
				}
				if _, rerr := output.Render(w.dest(cmd, sc.format), resp, sc.format, 0); err == nil {
					err = rerr
				}
			}
			return reportFailures(cmd.ErrOrStderr(), failed, len(t.repos), "repos", err)
		},
	}
	sc.register(cmd)
	w.register(cmd)
	cmd.Flags().BoolVar(&rebuild, "rebuild", false, "delete the workspace database and index from scratch")
	return cmd
}

// runIndex indexes the target repos, discovers their dependencies and
// indexes the package versions deps.db lacks, resolves what the repos
// changed (the
// whole workspace after a first index or a module or identity change,
// else only the files and names touched; see index.ResolvePending) and
// writes a warning to warn for every identity several repos provide. A
// repo that cannot be indexed is one line of failed and is left out of
// the rest of the run; the response is nil when no repo was indexed.
func runIndex(t target, rebuild bool, warn io.Writer) (resp *output.Response, failed []string, err error) {
	dbPath := t.cfg.DBPath(t.ws.Name)
	if rebuild {
		if err := removeDB(dbPath); err != nil {
			return nil, nil, err
		}
	}
	s, err := store.Open(dbPath)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = s.Close() }()
	opts := index.Options{Workspace: t.ws.Name, Exclude: t.ws.Exclude, RepoOrder: t.ws.RepoNames()}
	t, results, failed := indexRepos(s, t, opts)
	if len(t.repos) == 0 {
		return nil, failed, nil
	}
	resp = &output.Response{Header: output.Header{Fresh: true}}
	sec := output.Section{Title: "index"}
	for i, r := range t.repos {
		res := results[i]
		addHead(&resp.Header, t, r.Name, output.RepoHead{Name: r.Name, Branch: res.Branch, Commit: res.Commit,
			Dirty: res.Dirty, Fresh: true})
		sec.Entries = append(sec.Entries, output.Entry{Key: r.Name, Text: fmt.Sprintf("%s files:+%d ~%d -%d",
			output.HeadRef(res.Branch, res.Commit), res.FilesAdded, res.FilesIndexed-res.FilesAdded, res.FilesDeleted)})
	}
	// Dependencies come before resolution, so that one pass resolves the
	// references into the workspace and into its packages (deps spec
	// section 6).
	sum, err := deps.Sync(context.Background(), s, t.ws, t.repos, deps.Options{DB: t.cfg.DepsDBPath(), Full: true})
	if err != nil {
		return nil, failed, fmt.Errorf("index dependencies: %w", err)
	}
	if err := index.ResolvePending(s, opts); err != nil {
		return nil, failed, fmt.Errorf("resolve imports: %w", err)
	}
	if err := warnDuplicates(warn, s, opts.RepoOrder); err != nil {
		return nil, failed, err
	}
	resp.Sections = []output.Section{sec}
	if err := embedRepos(s, t, resp, warn); err != nil {
		return nil, failed, err
	}
	return resp, failed, reportDeps(s, t, resp, sum)
}

// reportDeps adds to the answer of an index run what its dependency
// sync left (deps spec section 9): a workspace with dependencies gets
// one more section, deps, with a line per repo.
func reportDeps(s *store.Store, t target, resp *output.Response, sum deps.Summary) error {
	listed, err := depsSection(s, t, t.cfg.DepsDBPath(), &output.Header{})
	if err != nil || len(listed.Entries) == 0 {
		return err
	}
	sec := output.Section{Title: "deps", AllTags: true}
	for _, r := range t.repos {
		counts := map[string]int{}
		for _, e := range listed.Entries {
			if e.Extra["repo"] == r.Name {
				counts[fmt.Sprint(e.Extra["state"])]++
			}
		}
		sec.Entries = append(sec.Entries, output.Entry{Key: r.Name, Text: fmt.Sprintf(
			"indexed:%d pending:%d missing:%d failed:%d skipped:%d unused:%d", counts[store.DepIndexed],
			counts[store.DepPending], counts[store.DepMissing], counts[store.DepFailed], counts[store.DepSkipped],
			counts[store.DepUnused])})
	}
	resp.Sections = append(resp.Sections, sec)
	resp.Note = depsNote(t, sum, listed)
	return nil
}

// warnDuplicates prints one line per identity that several repos provide
// (spec 7.3), naming the repos in config order and the first of them,
// which imports from any other repo resolve to.
func warnDuplicates(w io.Writer, s *store.Store, order []string) error {
	dups, err := s.DuplicateIdentities()
	if err != nil {
		return err
	}
	rank := make(map[string]int, len(order))
	for i, name := range order {
		rank[name] = i
	}
	for _, d := range dups {
		sort.SliceStable(d.Repos, func(i, j int) bool {
			ri, oki := rank[d.Repos[i]]
			rj, okj := rank[d.Repos[j]]
			return oki && (!okj || ri < rj)
		})
		if _, err := fmt.Fprintf(w, "warning: %s identity %s is provided by repos %s; imports from other repos resolve to %s\n",
			d.Lang, d.Identity, strings.Join(d.Repos, ", "), d.Repos[0]); err != nil {
			return err
		}
	}
	return nil
}

// removeDB deletes a SQLite database together with its WAL side files,
// refusing while a daemon serves it (see withRebuildLocks).
func removeDB(path string) error {
	return withRebuildLocks(path, func() error {
		for _, p := range []string{path, path + "-wal", path + "-shm"} {
			if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
		return nil
	})
}

// missingStatus describes a repo whose directory is gone: its index is
// kept and it is never fresh.
func missingStatus(s *store.Store, r *config.Repo) (output.Entry, repoState, error) {
	var st repoState
	row, err := s.RepoByPath(r.Path)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return output.Entry{}, st, err
	}
	st.row = row
	text := "missing: " + r.Path + " is gone"
	if row.IndexedAt > 0 {
		files, symbols, err := s.RepoStats(row.ID)
		if err != nil {
			return output.Entry{}, st, err
		}
		text += fmt.Sprintf("; index kept (%s files:%d symbols:%d)", output.HeadRef(row.Branch, row.Commit), files, symbols)
	}
	return output.Entry{Key: r.Name, Text: text}, st, nil
}

func newStatusCmd() *cobra.Command {
	var sc scope
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show index statistics per repo",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			t, err := sc.resolve()
			if err != nil {
				return err
			}
			resp, err := runStatus(t)
			if err != nil {
				return err
			}
			_, err = output.Render(cmd.OutOrStdout(), resp, sc.format, 0)
			return err
		},
	}
	sc.register(cmd)
	return cmd
}

// runStatus reports per repo the stored file and symbol counts, the
// config keys it defines and its config candidates no key matched, the
// head it was indexed at, the live dirty count, when it was indexed and
// its SCIP state (see scipStatus); a repo whose directory is gone says
// missing. Without a daemon a repo is fresh by the definition query
// headers use (see index.Check): its index reflects the working tree,
// uncommitted changes that were indexed included, and no file waits for
// re-extraction after an extractor update; with one, the daemon's answer
// decides (see withDaemon). Documentation upkeep is reported per repo
// from the daemon, or as needing it (see upkeepNeedsDaemon).
func runStatus(t target) (*output.Response, error) {
	s, err := store.Open(t.cfg.DBPath(t.ws.Name))
	if err != nil {
		return nil, err
	}
	defer func() { _ = s.Close() }()
	resp := &output.Response{}
	sec := output.Section{Title: "status"}
	opts := index.Options{Workspace: t.ws.Name, Exclude: t.ws.Exclude}
	states := make([]repoState, len(t.repos))
	for i, r := range t.repos {
		e, st, err := repoStatus(s, r, opts)
		if err == nil {
			e, err = withVectors(s, t, e, st.row.ID)
		}
		if err != nil {
			return nil, err
		}
		sec.Entries, states[i] = append(sec.Entries, e), st
	}
	resp.Sections = []output.Section{sec}
	if !withDaemon(resp, t.ws.Name, states) && t.ws.Upkeep.Enabled {
		upkeepNeedsDaemon(resp)
	}
	resp.Header = statusHeader(t, states)
	return resp, nil
}

// statusHeader sums the repo states into the response header.
func statusHeader(t target, states []repoState) output.Header {
	h := output.Header{Fresh: true}
	for i, st := range states {
		addHead(&h, t, t.repos[i].Name, output.RepoHead{Name: t.repos[i].Name, Branch: st.row.Branch,
			Commit: st.row.Commit, Dirty: st.dirty, Fresh: st.fresh})
	}
	return h
}

// addHead adds one target repo to a header the way query headers are
// built: a target of one repo (--repo, or a one-repo workspace) gets
// that repo's name, head and dirty count; a wider one a workspace header
// listing every repo, with the dirty counts summed and the stale repos
// named.
func addHead(h *output.Header, t target, name string, rh output.RepoHead) {
	h.Multi = len(t.ws.Repos) > 1
	h.Dirty += rh.Dirty
	h.Fresh = h.Fresh && rh.Fresh
	if len(t.repos) == 1 {
		h.Repo, h.Branch, h.Commit = name, rh.Branch, rh.Commit
		return
	}
	h.Workspace = t.ws.Name
	h.Repos = append(h.Repos, rh)
	if !rh.Fresh {
		h.Stale = append(h.Stale, name)
	}
}

type repoState struct {
	row   store.Repo
	dirty int
	fresh bool
}

func repoStatus(s *store.Store, r *config.Repo, opts index.Options) (output.Entry, repoState, error) {
	if info, err := os.Stat(r.Path); err != nil || !info.IsDir() {
		return missingStatus(s, r)
	}
	var st repoState
	var err error
	st.row, err = s.RepoByPath(r.Path)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return output.Entry{}, st, err
	}
	f, err := index.Check(s, r, opts)
	if err != nil {
		return output.Entry{}, st, err
	}
	st.dirty, st.fresh = f.Dirty, f.Fresh
	if st.row.IndexedAt == 0 {
		return output.Entry{Key: r.Name, Text: "not indexed"}, st, nil
	}
	text, err := indexedStatus(s, st.row, f)
	return output.Entry{Key: r.Name, Text: text}, st, err
}

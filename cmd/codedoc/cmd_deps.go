package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/deps"
	"github.com/muralx/codedoc/internal/embed"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/query"
	"github.com/muralx/codedoc/internal/store"
)

// depsQueryBudget is how long a query, and the codedoc deps listing,
// spend indexing missing package versions (deps spec section 9).
const depsQueryBudget = 2 * time.Second

// newDepsCmd builds codedoc deps. With a module it is the module import
// query of the v1 design; without one it lists the third-party packages
// the workspace depends on (deps spec section 7), which the index and
// prune subcommands maintain. A module that is literally named index or
// prune is addressed as repo:index.
func newDepsCmd() *cobra.Command {
	cmd := newGraphCmd("deps [module]", "List the modules a module imports; without a module, the third-party packages "+
		"the workspace depends on",
		"  codedoc deps github.com/fixture/app --cross-repo\n  codedoc deps go-lib:service\n"+
			"  codedoc deps --repo web\n  codedoc deps index\n  codedoc deps prune",
		graphFlags{depth: true, crossRepo: true}, query.Deps)
	cmd.Long = "With a module: list the modules it imports (a module identity, repo:dir or a directory).\n\n" +
		"Without one: list the package versions the manifests of the workspace resolve to (npm, Go modules), each " +
		"indexed, pending, missing (declared but not installed), failed (with the error), skipped (a workspace package) " +
		"or unused (installed, but no source file imports it, so it is not indexed; deps.include indexes it anyway)."
	cmd.Args = cobra.MaximumNArgs(1)
	imports := cmd.RunE
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 1 {
			return imports(cmd, args)
		}
		q, err := queryFlagsOf(cmd)
		if err != nil {
			return err
		}
		return renderDeps(cmd, q, deps.Options{Deadline: time.Now().Add(depsQueryBudget)}, nil)
	}
	var q queryFlags
	var w writeFlags
	index := &cobra.Command{
		Use:   "index",
		Short: "Discover the dependencies again and index every package version deps.db lacks",
		Long: "Discover the dependencies again and index every package version deps.db lacks.\n\nIt prints nothing " +
			"when it succeeds: the exit status is the result. --verbose prints the listing codedoc deps prints. " +
			"Package versions that failed, or a manifest that could not be read, are one warning line on stderr.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return renderDeps(cmd, &q, deps.Options{Full: true}, &w)
		},
	}
	q.register(index)
	w.register(index)
	index.Flags().BoolVar(&embedDepVectors, "embed", false,
		"also embed the vectors the indexed packages lack, for search --deps by meaning (the daemon does it in the background)")
	cmd.AddCommand(index, newDepsPruneCmd())
	return cmd
}

// queryFlagsOf reads the shared query flags a command registered.
func queryFlagsOf(cmd *cobra.Command) (*queryFlags, error) {
	var q queryFlags
	var err error
	fl := cmd.Flags()
	for name, dst := range map[string]*string{"workspace": &q.sc.workspace, "repo": &q.sc.repo, "format": &q.sc.format,
		"out": &q.out} {
		if *dst, err = fl.GetString(name); err != nil {
			return nil, err
		}
	}
	if q.budget, err = fl.GetInt("budget"); err != nil {
		return nil, err
	}
	q.offset, err = fl.GetInt("offset")
	if err == nil && (q.offset < 0 || q.budget < 0) {
		err = errors.New("--offset and --budget must not be negative")
	}
	return &q, err
}

// renderDeps resolves the scope, syncs and lists the dependencies (see
// runDeps) and renders the listing like a query answer. For a write
// (codedoc deps index, w set) the listing is printed only when asked for
// (see writeFlags), and otherwise a sync that left failures warns in one
// line (see warnDeps).
func renderDeps(cmd *cobra.Command, q *queryFlags, opts deps.Options, w *writeFlags) error {
	t, err := q.sc.resolve()
	if err != nil {
		return err
	}
	opts.DB = t.cfg.DepsDBPath()
	resp, err := runDeps(t, opts)
	if err != nil {
		return err
	}
	sec := &resp.Sections[0]
	sec.Offset = min(q.offset, len(sec.Entries))
	sec.Entries = sec.Entries[sec.Offset:]
	if w == nil {
		return q.render(cmd.OutOrStdout(), resp)
	}
	if !w.shows(q.sc.format) {
		if err := warnDeps(cmd.ErrOrStderr(), resp.Note); err != nil {
			return err
		}
	}
	return q.render(w.dest(cmd, q.sc.format), resp)
}

// depsPruneLong is the help of codedoc deps prune.
const depsPruneLong = "Delete from deps.db every package version that neither a workspace of the config nor a workspace " +
	"database recorded in deps.db as its user resolves to. --dry-run lists them without deleting.\n\n" +
	"Deleting prints nothing when it succeeds: the exit status is the result. --verbose lists what was " +
	"deleted. --dry-run always prints its list: it is a question, not a write."

func newDepsPruneCmd() *cobra.Command {
	var q queryFlags
	var f pruneFlags
	var w writeFlags
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Delete from deps.db every package version no workspace uses",
		Long:  depsPruneLong,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := output.CheckFormat(q.sc.format); err != nil {
				return err
			}
			if q.offset < 0 || q.budget < 0 {
				return errors.New("--offset and --budget must not be negative")
			}
			cfg, err := config.Load(config.DefaultPath())
			if err != nil {
				return err
			}
			resp, err := runDepsPrune(cfg, f)
			if err != nil {
				return err
			}
			sec := &resp.Sections[0]
			sec.Offset = min(q.offset, len(sec.Entries))
			sec.Entries = sec.Entries[sec.Offset:]
			return q.render(f.dest(cmd, &w, q.sc.format), resp)
		},
	}
	w.register(cmd)
	fl := cmd.Flags()
	fl.StringVar(&q.sc.format, "format", "text", "output format: text or json")
	fl.IntVar(&q.budget, "budget", 8000, "output budget in bytes (0 means unlimited)")
	fl.IntVar(&q.offset, "offset", 0, "skip the first N pruned package versions")
	fl.StringVar(&q.out, "out", "", "write the full result to this file and print a summary")
	fl.BoolVar(&f.all, "all", false, "delete every package version: empty deps.db")
	fl.BoolVar(&f.dryRun, "dry-run", false, "list what would be deleted and delete nothing")
	return cmd
}

// runDeps syncs the dependency uses of the target repos (see deps.Sync)
// and lists them: one entry per manifest and package with its state, the
// declared range, the symbols indexed and, for a failed package, the
// error. The note counts what is still pending and names the manifests
// that could not be read and the deps.include names found nowhere. A
// full sync (codedoc deps index) also embeds the vectors the packages
// lack, and the note counts them; asked to embed without an embedder it
// fails before it syncs (see depEmbedder). It ends with the number of
// unused packages and the way to index one anyway (see unusedNote).
func runDeps(t target, opts deps.Options) (*output.Response, error) {
	var e embed.Embedder
	if opts.Full && embedDepVectors {
		var err error
		if e, err = depEmbedder(t.cfg.Embed.ModelDir); err != nil {
			return nil, err
		}
		defer func() { _ = e.Close() }()
	}
	s, err := store.Open(t.cfg.DBPath(t.ws.Name))
	if err != nil {
		return nil, err
	}
	defer func() { _ = s.Close() }()
	sum, err := deps.Sync(context.Background(), s, t.ws, t.repos, opts)
	if err != nil {
		return nil, err
	}
	vectors := 0
	if e != nil {
		if vectors, err = embedNewDeps(s, t, e); err != nil {
			return nil, err
		}
	}
	resp := &output.Response{Header: output.Header{Fresh: true}}
	sec, err := depsSection(s, t, opts.DB, &resp.Header)
	if err != nil {
		return nil, err
	}
	resp.Sections = []output.Section{sec}
	resp.Note = depsNote(t, sum, sec)
	if vectors > 0 {
		resp.Note = strings.TrimPrefix(resp.Note+fmt.Sprintf(". embedded %d vector(s)", vectors), ". ")
	}
	if note := unusedNote(sec); note != "" {
		resp.Note = strings.TrimPrefix(resp.Note+". "+note, ". ")
	}
	return resp, nil
}

// embedFailed starts the one line codedoc deps index --embed fails
// with when it cannot embed: it was asked to, so silence would read as
// done.
const embedFailed = "deps index --embed: nothing was embedded: "

// depEmbedder returns the embedder codedoc deps index --embed embeds
// with, or the one line saying why there is none and what to do.
func depEmbedder(modelDir string) (embed.Embedder, error) {
	e := embed.Configured(modelDir, nil)
	switch {
	case e != nil:
		return e, nil
	case os.Getenv(embed.EmbedderEnv) == "off":
		return nil, errors.New(embedFailed + "embeddings are off (" + embed.EmbedderEnv + "=off); unset it and run the command again")
	}
	return nil, errors.New(embedFailed + "no embedding model is configured; set embed.model_dir in the config " +
		"(or CODEDOC_MODEL_DIR) to the directory make download-model fills")
}

// embedNewDeps embeds with e the vectors the package versions of the
// target repos still lack (deps spec section 8) and returns how many it
// wrote. A model that does not load is an error naming the cause.
func embedNewDeps(s *store.Store, t target, e embed.Embedder) (int, error) {
	res, err := deps.EmbedVectors(s, t.cfg.DepsDBPath(), e, t.repos, 0)
	if err == nil && res.Unavailable {
		_, cause := e.Embed("")
		return 0, fmt.Errorf(embedFailed+"%w; check embed.model_dir in the config (or CODEDOC_MODEL_DIR)", cause)
	}
	return res.Symbols + res.Modules, err
}

// embedDepVectors is the --embed flag of codedoc deps index.
var embedDepVectors bool

// depsSection lists the dependency uses of the target repos and adds
// each repo's indexed head to h.
func depsSection(s *store.Store, t target, db string, h *output.Header) (output.Section, error) {
	sec := output.Section{Title: "deps", AllTags: true}
	infos, err := packageInfos(db)
	if err != nil {
		return sec, err
	}
	for _, r := range t.repos {
		row, err := s.RepoByPath(r.Path)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return sec, err
		}
		addHead(h, t, r.Name, output.RepoHead{Name: r.Name, Branch: row.Branch, Commit: row.Commit, Fresh: true})
		uses, err := s.DepUses(row.ID)
		if err != nil {
			return sec, err
		}
		for _, u := range uses {
			if row.ID != 0 {
				sec.Entries = append(sec.Entries, depEntry(t, r, u, infos[u.PackageID()]))
			}
		}
	}
	return sec, nil
}

// packageInfos returns what deps.db holds, by package id; nothing when
// the database does not exist yet.
func packageInfos(db string) (map[string]deps.PackageInfo, error) {
	out := map[string]deps.PackageInfo{}
	if _, err := os.Stat(db); errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	d, err := deps.Open(db)
	if err != nil {
		return nil, err
	}
	defer func() { _ = d.Close() }()
	infos, err := d.Packages()
	for _, p := range infos {
		out[p.ID] = p
	}
	return out, err
}

// depEntry renders one dependency use: the package id (eco/name for a
// use without a version), the manifest, and the state with its details.
func depEntry(t target, r *config.Repo, u store.DepUse, info deps.PackageInfo) output.Entry {
	e := output.Entry{Key: u.PackageID(), Loc: u.Manifest, Text: u.State}
	if e.Key == "" {
		e.Key = u.Eco + "/" + u.Name
	}
	if len(t.repos) > 1 {
		e.Loc = r.Name + ":" + u.Manifest
	}
	if u.Package != "" {
		e.Text += " as:" + u.Name
	}
	if u.Declared != "" {
		e.Text += " declared:" + u.Declared
	}
	if u.State == store.DepIndexed {
		e.Text += fmt.Sprintf(" symbols:%d vectors:%d/%d", info.Symbols, info.Embedded, info.Embeddable)
	}
	if u.State == store.DepIndexed && info.Only != nil {
		e.Text += fmt.Sprintf(" modules:%d", len(info.Only))
	}
	if u.Error != "" {
		e.Text += " error: " + strings.ReplaceAll(u.Error, "\n", " ")
	}
	if info.Truncated {
		e.Tags = []string{"truncated"}
	}
	e.Extra = map[string]any{"repo": r.Name, "manifest": u.Manifest, "eco": u.Eco, "name": u.Name, "version": u.Version,
		"declared": u.Declared, "state": u.State, "dir": u.Root(), "error": u.Error, "symbols": info.Symbols,
		"truncated": info.Truncated, "dev": u.Dev,
		"vectors": map[string]int{"embedded": info.Embedded, "total": info.Embeddable}}
	if u.State == store.DepIndexed && info.Only != nil {
		e.Extra["modules"] = info.Only
	}
	return e
}

// depsNote sums a sync up in one line: what it indexed, what failed or
// is pending, the manifests it could not read and the deps.include
// entries no manifest of the listed repos resolves.
func depsNote(t target, sum deps.Summary, sec output.Section) string {
	var notes []string
	if sum.Indexed > 0 {
		notes = append(notes, fmt.Sprintf("indexed %d package version(s)", sum.Indexed))
	}
	if sum.Failed > 0 {
		notes = append(notes, fmt.Sprintf("%d failed", sum.Failed))
	}
	if sum.Pending > 0 {
		notes = append(notes, fmt.Sprintf("deps: %d pending; run codedoc deps index", sum.Pending))
	}
	for _, p := range sum.Problems {
		notes = append(notes, "unreadable manifest "+p)
	}
	for _, entry := range t.ws.Deps.Include {
		if !includeFound(entry, sec) && t.ws.Deps.On() {
			notes = append(notes, "deps.include: "+entry+" is not installed")
		}
	}
	return strings.Join(notes, ". ")
}

// unusedNote says how many listed packages are unused and how to index
// one anyway; "" when none is.
func unusedNote(sec output.Section) string {
	unused := 0
	for _, e := range sec.Entries {
		if e.Extra["state"] == store.DepUnused {
			unused++
		}
	}
	if unused == 0 {
		return ""
	}
	return fmt.Sprintf("%d unused: declared but imported by no source file, so not indexed; "+
		"name a package in deps.include to index it anyway", unused)
}

// includeFound reports whether a deps.include entry, a package name or
// a pattern, matches a listed package.
func includeFound(entry string, sec output.Section) bool {
	for _, e := range sec.Entries {
		if config.MatchPackage(entry, fmt.Sprint(e.Extra["name"])) {
			return true
		}
	}
	return false
}

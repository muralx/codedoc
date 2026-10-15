package main

import (
	"errors"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/embed"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/query"
	"github.com/muralx/codedoc/internal/store"
)

// summaryBudget is the stdout budget when --out writes the full result.
const summaryBudget = 600

// queryFlags are the flags every query shares (spec section 9).
type queryFlags struct {
	sc     scope
	budget int
	offset int
	out    string
	// deps is --deps, registered by the queries that include the
	// third-party packages of the workspace (see registerDeps).
	deps bool
	// keys and detail are --keys and --detail: stable keys in place of
	// names, and every tag, signature and doc, in text output.
	keys, detail bool
	// strict is --strict: an address that names nothing or several
	// symbols is an error, as before answers offered the nearest.
	strict bool
	// context holds -C, -B and -A of the queries that print lines of
	// files around what they list (see registerContext).
	context       string
	before, after int
	// stdout, when set, replaces the command's stdout as the place the
	// answer is rendered to: a write discards its answer there unless it
	// was asked for (see writeFlags.dest).
	stdout io.Writer
	// cmd is the command the flags are registered on: an answer built
	// outside the query layer takes its follow-up command from it.
	cmd *cobra.Command
}

func (q *queryFlags) register(cmd *cobra.Command) {
	q.cmd = cmd
	q.sc.register(cmd)
	cmd.Flags().IntVar(&q.budget, "budget", 8000, "output budget in bytes (0 means unlimited)")
	cmd.Flags().IntVar(&q.offset, "offset", 0, "skip the first N ranked entries of each section")
	cmd.Flags().StringVar(&q.out, "out", "", "write the full result to this file and print a summary")
	cmd.Flags().BoolVar(&q.keys, "keys", false, "print stable keys in place of names")
	cmd.Flags().BoolVar(&q.detail, "detail", false, "print every tag, signature and doc sentence")
	cmd.Flags().BoolVar(&q.strict, "strict", false,
		"fail on an address that names nothing (exit 1) or several symbols (exit 3, candidates listed) "+
			"instead of answering for the nearest or the best candidate")
}

// registerContext adds -C/--context, -B and -A to a query whose entries
// are lines of files: how many lines are shown around each of them.
func (q *queryFlags) registerContext(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&q.context, "context", "C", "",
		"lines shown around each line listed: N, symbol (the enclosing symbol or section) or file (N may leave it)")
	cmd.Flags().IntVarP(&q.before, "before", "B", -1, "lines shown before each line listed")
	cmd.Flags().IntVarP(&q.after, "after", "A", -1, "lines shown after each line listed")
}

// registerDeps adds --deps to a query that can answer from the
// dependency index (deps spec section 7).
func (q *queryFlags) registerDeps(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&q.deps, "deps", false,
		"include the third-party packages the workspace depends on, tagged [dep name@version], "+
			"also for a name the workspace has (one it lacks is looked up there without the flag)")
}

// run builds the query context from the flags, runs fn and renders its
// response. A response that comes with an error (the candidates of an
// ambiguous address under --strict) is still printed before the error is
// returned; the nearest candidates of an address that matched nothing
// (query.ErrNearest) are an answer, not an error.
func (q *queryFlags) run(cmd *cobra.Command, fn func(*query.Ctx) (*output.Response, error)) error {
	if q.offset < 0 || q.budget < 0 {
		return errors.New("--offset and --budget must not be negative")
	}
	lines, err := query.ParseContext(q.context, q.before, q.after)
	if err != nil {
		return err
	}
	t, err := q.sc.resolve()
	if err != nil {
		return err
	}
	s, err := store.Open(t.cfg.DBPath(t.ws.Name))
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	db := t.cfg.DBPath(t.ws.Name)
	c := &query.Ctx{Store: s, Workspace: t.ws, Offset: q.offset, Budget: q.budget, DepsDB: t.cfg.DepsDBPath(),
		Embedder:    embed.Configured(t.cfg.Embed.ModelDir, cmd.ErrOrStderr()),
		DaemonFresh: func(ws string, r *config.Repo) bool { return daemonFresh(ws, r, db) }}
	c.Deps, c.Keys, c.Detail, c.Command, c.Context = q.deps, q.keys, q.detail, commandLine(cmd), lines
	c.Scope, c.Strict = q.sc.flags(), q.strict
	defer func() { _ = c.Close() }()
	if c.Embedder != nil {
		defer func() { _ = c.Embedder.Close() }()
	}
	q.place(c, t)
	resp, qerr := fn(c)
	if resp != nil {
		if err := q.render(q.answerTo(cmd), resp); err != nil {
			return err
		}
	}
	if errors.Is(qerr, query.ErrNearest) {
		return nil // the nearest candidates are the answer
	}
	return qerr
}

// place tells the context where the query runs: the working directory,
// the repo --repo names or the one around the working directory, and no
// budget when the whole answer goes to a file.
func (q *queryFlags) place(c *query.Ctx, t target) {
	if q.out != "" {
		c.Budget = 0
	}
	if cwd, err := os.Getwd(); err == nil {
		c.Cwd = cwd
	}
	if q.sc.repo != "" {
		c.Repo, c.Filter = t.repos[0], true
	} else {
		c.Repo = cwdRepo(t)
	}
}

// answerTo returns where the answer of the command is rendered: its
// stdout, unless the flags name another place.
func (q *queryFlags) answerTo(cmd *cobra.Command) io.Writer {
	if q.stdout != nil {
		return q.stdout
	}
	return cmd.OutOrStdout()
}

// cwdRepo returns the repo of the chosen workspace that contains the
// working directory, or nil.
func cwdRepo(t target) *config.Repo {
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}
	ws, r, err := t.cfg.Resolve(cwd)
	if err != nil || ws != t.ws {
		return nil
	}
	return r
}

// render prints the response under the budget, or with --out writes it
// unlimited to the file and prints a short text summary that ends with
// the file's path.
func (q *queryFlags) render(w io.Writer, resp *output.Response) error {
	if resp.Command == "" && q.cmd != nil {
		resp.Command = commandLine(q.cmd)
	}
	if q.out == "" {
		_, err := output.Render(w, resp, q.sc.format, q.budget)
		return err
	}
	f, err := os.Create(q.out)
	if err != nil {
		return err
	}
	if _, err := output.Render(f, resp, q.sc.format, 0); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	tail := "full result: " + q.out + "\n"
	if _, err := output.RenderSummary(w, resp, max(1, summaryBudget-len(tail))); err != nil {
		return err
	}
	_, err = io.WriteString(w, tail)
	return err
}

func newOutlineCmd() *cobra.Command {
	var q queryFlags
	var o query.OutlineOpts
	cmd := &cobra.Command{
		Use:     "outline <path>",
		Short:   "List the symbols of a file, the headings of a document or the files of a directory",
		Example: "  codedoc outline store/store.go\n  codedoc outline store/store.go --detail\n  codedoc outline go-lib:store",
		Long: "List what a path holds. A file: its symbols in line order, each with its lines, signature and first " +
			"doc sentence (--doc: the whole doc; --detail: kind, fan-in and doc state), members folded into " +
			"[+N members]. A document: its heading tree. A directory: its files and subdirectories. A path that " +
			"names nothing is answered for the nearest indexed path; --strict fails instead.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return q.run(cmd, func(c *query.Ctx) (*output.Response, error) { return query.Outline(c, args[0], o) })
		},
	}
	q.register(cmd)
	q.registerDeps(cmd)
	cmd.Flags().StringVar(&o.Sort, "sort", "name", "order of a directory outline: "+strings.Join(query.LsSorts, ", "))
	cmd.Flags().BoolVar(&o.Reverse, "reverse", false, "invert the order of a directory outline")
	cmd.Flags().BoolVar(&o.Doc, "doc", false, "print the whole doc of each symbol, not its first sentence")
	return cmd
}

func newFindCmd() *cobra.Command {
	var q queryFlags
	var o query.FindOpts
	cmd := &cobra.Command{
		Use:   "find <name|glob>",
		Short: "Find symbols by name, qualified name or glob",
		Example: "  codedoc find MemStore.Get\n  codedoc find '*Store' --kind struct --detail\n" +
			"  codedoc find Server --deps",
		Long: "List the symbols a name, a qualified name or a glob (*, ?, [...]) matches: name, location, signature " +
			"and first doc sentence (--doc: the whole doc; --detail: kind, fan-in and doc state).\n\nA name no " +
			"workspace symbol has is looked up in the dependencies, and one nothing has is answered by codedoc " +
			"search; several words are answered by codedoc where. A glob that matches nothing, and any pattern " +
			"under --strict, is an empty answer.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return q.run(cmd, func(c *query.Ctx) (*output.Response, error) { return query.Find(c, args[0], o) })
		},
	}
	q.register(cmd)
	q.registerDeps(cmd)
	cmd.Flags().StringVar(&o.Kind, "kind", "", "symbol kind (function, method, struct, interface, class, ...; "+
		"section or document match headings by substring)")
	cmd.Flags().StringVar(&o.Lang, "lang", "", query.LangHelp)
	cmd.Flags().BoolVar(&o.Doc, "doc", false, "print the whole doc of each symbol, not its first sentence")
	return cmd
}

func newSymbolCmd() *cobra.Command {
	var q queryFlags
	var o query.SymbolOpts
	var body bool
	cmd := &cobra.Command{
		Use:   "symbol <address>...",
		Short: "Print a symbol or document section, or several: location and source with the doc comment",
		Example: "  codedoc symbol MemStore.Get\n  codedoc symbol store/store.go#MemStore.Get --context 3\n" +
			"  codedoc symbol MemStore.Get --no-body\n  codedoc symbol \"docs/design.md#6.2\"\n" +
			"  codedoc symbol store/store.go:28\n  codedoc symbol MemStore.Get MemStore.Put NewMemStore",
		Long: "Print one symbol: its location and the lines of its source, starting at its doc comment. A source " +
			"larger than the budget is answered with the outline of its members, or cut and continued with codedoc " +
			"lines. --no-body prints the location, signature and doc only; --detail adds kind, container, module, " +
			"fan-in and fan-out.\n\nAddresses: a stable key, repo:path#Name, path#Name in the current repo, or a bare name.\n" +
			"A section of a document is doc.md#Heading (the heading text, any case, its numbering such as 6.2, a prefix " +
			"or a substring); doc.md alone is the document.\n" +
			"path:LINE is the innermost symbol or section enclosing that line.\n\nSeveral addresses are answered " +
			"together under the one budget: the sources that fit their share are whole, the larger ones are cut and " +
			"continued (or outlined), and an address that names no symbol is one line.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return q.run(cmd, func(c *query.Ctx) (*output.Response, error) {
				if len(args) > 1 {
					return query.Symbols(c, args, o)
				}
				return query.Symbol(c, args[0], o)
			})
		},
	}
	q.register(cmd)
	q.registerDeps(cmd)
	cmd.Flags().BoolVar(&body, "body", true, "print the source (the default; kept for older callers)")
	cmd.Flags().BoolVar(&o.NoBody, "no-body", false, "print only the location, the signature and the doc")
	cmd.Flags().BoolVar(&o.DocOnly, "doc-only", false, "print only the signature and the doc")
	cmd.Flags().IntVar(&o.Context, "context", 0, "print N extra lines before and after the source")
	cmd.MarkFlagsMutuallyExclusive("doc-only", "body")
	cmd.MarkFlagsMutuallyExclusive("doc-only", "context")
	cmd.MarkFlagsMutuallyExclusive("no-body", "body")
	cmd.MarkFlagsMutuallyExclusive("no-body", "context")
	return cmd
}

func newConfigCmd() *cobra.Command {
	var q queryFlags
	var o query.ConfigOpts
	cmd := &cobra.Command{
		Use:   "config <key|glob>",
		Short: "Show where a config key is defined, referenced and read by code",
		Example: "  codedoc config LIB_MODE\n  codedoc config 'lib.*' --defines\n" +
			"  codedoc config '*_MODE' --reads --repo go-lib",
		Long: "Show where a config key is defined (with value previews), which config files and templates refer " +
			"to it, and which code symbols read it.\n\nA glob lists the matching keys first, then the sections of each key.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return q.run(cmd, func(c *query.Ctx) (*output.Response, error) { return query.Config(c, args[0], o) })
		},
	}
	q.register(cmd)
	cmd.Flags().BoolVar(&o.Defines, "defines", false, "show the definitions")
	cmd.Flags().BoolVar(&o.Reads, "reads", false, "show the code that reads the key")
	return cmd
}

// commandLine is the command line that ran cmd, without --offset and
// quoted for a shell: what an omission line needs to name the exact
// follow-up.
func commandLine(cmd *cobra.Command) string {
	parts := []string{cmd.CommandPath()}
	for _, a := range cmd.Flags().Args() {
		parts = append(parts, shellQuote(a))
	}
	cmd.Flags().Visit(func(f *pflag.Flag) {
		switch {
		case f.Name == "offset":
		case f.Value.Type() == "bool":
			parts = append(parts, "--"+f.Name)
		default:
			parts = append(parts, "--"+f.Name, shellQuote(f.Value.String()))
		}
	})
	return strings.Join(parts, " ")
}

// plainWord matches an argument a shell reads as one word unquoted.
var plainWord = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./#-]+$`)

// shellQuote quotes an argument for a POSIX shell when it needs it.
func shellQuote(s string) string {
	if plainWord.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

package main

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/query"
)

// newUnderstandCmds returns the commands that answer "what is where" and
// "how does it work": search, where, explain and flow, and the ones that
// answer a whole need in one call (see newCompositeCmds).
func newUnderstandCmds() []*cobra.Command {
	return append([]*cobra.Command{newSearchCmd(), newWhereCmd(), newExplainCmd(), newFlowCmd()}, newCompositeCmds()...)
}

func newSearchCmd() *cobra.Command {
	var q queryFlags
	var o query.SearchOpts
	cmd := &cobra.Command{
		Use:   "search <text>",
		Short: "Search symbols, document sections and modules by keywords and meaning, with the lines that match",
		Example: "  codedoc search \"persist values by key\"\n  codedoc search 'token refresh retry' --kind method --lang go\n" +
			"  codedoc search 'storage' --kind module",
		Long: "Search code, documents and modules: keywords in names, signatures, docs and section text, fused with " +
			"embedding similarity when a model is configured (embed.model_dir or CODEDOC_MODEL_DIR; without one the " +
			"search is text-only and says so). A hit is its name, location, signature and first doc sentence, then " +
			"the lines of its file that match, two lines around them (-C, -A, -B; --context symbol for the whole " +
			"symbol). 20 hits a page (--limit, --offset). codedoc where answers with the top five locations.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			text := strings.Join(args, " ")
			return q.run(cmd, func(c *query.Ctx) (*output.Response, error) { return query.Search(c, text, o) })
		},
	}
	q.register(cmd)
	q.registerDeps(cmd)
	q.registerContext(cmd)
	cmd.Flags().StringVar(&o.Kind, "kind", "", "symbol kind (function, method, struct, ...), or module for modules only")
	cmd.Flags().StringVar(&o.Lang, "lang", "", query.LangHelp)
	cmd.Flags().IntVar(&o.Limit, "limit", 20, "results per page; --offset continues")
	return cmd
}

func newExplainCmd() *cobra.Command {
	var q queryFlags
	cmd := &cobra.Command{
		Use:   "explain <symbol|path>",
		Short: "Show the doc stack of a symbol, module or file: repo, modules, container, symbol, top callers and callees",
		Example: "  codedoc explain MemStore.Get\n  codedoc explain go-lib:store\n" +
			"  codedoc explain github.com/fixture/lib/store\n  codedoc explain go-lib:store/store.go",
		Long: "Explain a symbol: the repo's README summary, the module chain down to the symbol's module, the " +
			"container's doc, the symbol's signature, doc and doc state, then its top callers and callees by " +
			"fan-in.\n\nA path (repo:dir, repo:file, a path in the current repo) or a module identity explains " +
			"the module or file and lists its main symbols.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return q.run(cmd, func(c *query.Ctx) (*output.Response, error) { return query.Explain(c, args[0]) })
		},
	}
	q.register(cmd)
	q.registerDeps(cmd)
	return cmd
}

func newFlowCmd() *cobra.Command {
	var q queryFlags
	var o query.FlowOpts
	cmd := &cobra.Command{
		Use:     "flow <symbol>",
		Short:   "Show the call tree below a symbol, each node with its signature and one-line doc",
		Example: "  codedoc flow main --depth 4\n  codedoc flow Service.Lookup --documented-only",
		Long: "Show the call tree below an entry symbol, depth first in call order. Ambiguous calls list their " +
			"candidates and are not expanded; calls back up the current path are marked cycle. The tree stops " +
			"at 200 nodes.\n\nAddresses: a stable key, repo:path#Name, path#Name in the current repo, or a bare name.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return q.run(cmd, func(c *query.Ctx) (*output.Response, error) { return query.Flow(c, args[0], o) })
		},
	}
	q.register(cmd)
	cmd.Flags().IntVar(&o.Depth, "depth", 3, "how many calls deep to go")
	cmd.Flags().BoolVar(&o.DocumentedOnly, "documented-only", false,
		"leave out undocumented symbols, attaching their documented callees to the nearest documented caller")
	return cmd
}

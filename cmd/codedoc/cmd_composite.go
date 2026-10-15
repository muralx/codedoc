package main

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/query"
)

// impactBudget is the default budget of impact: a summary whose
// sections each name the command that prints the rest of them.
const impactBudget = 3000

// newCompositeCmds returns the commands that answer a whole need in one
// call (efficiency spec section 5): impact, how and api.
func newCompositeCmds() []*cobra.Command {
	return []*cobra.Command{newImpactCmd(), newHowCmd(), newAPICmd()}
}

// flags renders the flags that choose the workspace and the repo as
// they follow a command, "" when neither was given: what a follow-up
// command needs to run in the same scope.
func (sc *scope) flags() string {
	out := ""
	if sc.workspace != "" {
		out += " --workspace " + shellQuote(sc.workspace)
	}
	if sc.repo != "" {
		out += " --repo " + shellQuote(sc.repo)
	}
	return out
}

func newImpactCmd() *cobra.Command {
	var q queryFlags
	var o query.ImpactOpts
	cmd := &cobra.Command{
		Use:   "impact <symbol>",
		Short: "Show what a change to a symbol touches: callers, implementations, tests, mentions, config, exports",
		Example: "  codedoc impact MemStore.Get\n  codedoc impact Service.Lookup --budget 8000\n" +
			"  codedoc impact Store.Get --only mentions --offset 5",
		Long: "Answer \"I am about to change this\" in one call: the symbol with its signature and the first sentence " +
			"of its doc, then its callers as their call lines (grouped by file, production code first; a file with " +
			"more than ten call sites prints three and a count; calls that may go elsewhere under \"possibly\"), what " +
			"implements or overrides it, the tests that reach it (directly, then via callers), the document sections " +
			"and doc comments that name it, the config keys it reads, and whether it is exported and which other " +
			"repos use it.\n\nSections without entries are left out. The budget is shared: no section costs another " +
			"its place, and a section that is cut ends with the exact command for the rest of it. --only prints one " +
			"section (" + strings.Join(query.ImpactSections, ", ") + "), which --offset pages. A dependency symbol " +
			"is answered with the workspace code that calls or refers to it, and its tests.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return q.run(cmd, func(c *query.Ctx) (*output.Response, error) { return query.Impact(c, args[0], o) })
		},
	}
	q.register(cmd)
	q.registerContext(cmd)
	q.budget = impactBudget
	cmd.Flags().Lookup("budget").DefValue = "3000"
	cmd.Flags().StringVar(&o.Only, "only", "", "print one section: "+strings.Join(query.ImpactSections, ", "))
	return cmd
}

func newHowCmd() *cobra.Command {
	var q queryFlags
	cmd := &cobra.Command{
		Use:   "how <symbol|path>",
		Short: "Explain a symbol or a file and show the first two levels of its call tree",
		Example: "  codedoc how Service.Lookup\n  codedoc how go-app:main.go\n" +
			"  codedoc how github.com/fixture/lib/store",
		Long: "Answer \"how does this work\" in one call: what explain says (the doc stack from the repo down to " +
			"the symbol, its top callers), then the first two levels of the call tree flow prints, under one " +
			"shared budget. A code file gets the tree of each of its entry functions (the ones nothing calls); a " +
			"directory, a module, a document or a dependency is answered as explain answers it.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return q.run(cmd, func(c *query.Ctx) (*output.Response, error) { return query.How(c, args[0]) })
		},
	}
	q.register(cmd)
	q.registerDeps(cmd)
	return cmd
}

func newAPICmd() *cobra.Command {
	var q queryFlags
	var o query.APIOpts
	cmd := &cobra.Command{
		Use:   "api <package> [name]",
		Short: "Show the surface of a dependency: its modules and exported names, or one type with its members",
		Example: "  codedoc api github.com/fixture/dep\n  codedoc api fixture-dep@1.2.0 Server\n" +
			"  codedoc api @scope/pkg/server --doc",
		Long: "Show what a third-party package offers, without --deps. With the package alone (its name as the " +
			"manifest declares it, optionally name@version, or an import specifier below it): the first sentence " +
			"of its README, the exported top-level names of its modules (the main module, then the ones the " +
			"workspace imports most) as name, kind and for a function its signature, and the list of its " +
			"modules. With a name: that type or function with its doc and, for a type, its members with their " +
			"signatures, the inherited ones among them named after the type that declares them.\n\nA package " +
			"that is declared but not indexed says why and what to do; a name the package does not export lists " +
			"the nearest names. --doc adds the first doc sentence of every name.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) > 1 {
				name = args[1]
			}
			return q.run(cmd, func(c *query.Ctx) (*output.Response, error) { return query.API(c, args[0], name, o) })
		},
	}
	q.register(cmd)
	cmd.Flags().BoolVar(&o.Doc, "doc", false, "print the first doc sentence of every name (--detail does too)")
	return cmd
}

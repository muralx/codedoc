package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/query"
)

// graphQuery is a query over one address with graph options.
type graphQuery func(*query.Ctx, string, query.GraphOpts) (*output.Response, error)

// graphFlags selects the graph options a command accepts.
type graphFlags struct {
	depth, noTests, groupBy, kind, transitive, crossRepo bool
	// sites: the entries are lines of files, so -C, -B and -A apply.
	sites bool
	// deps: the address may name a dependency symbol, so --deps applies.
	deps bool
}

// newGraphCmd builds a command running fn on its one argument with the
// shared query flags and the graph flags in f.
func newGraphCmd(use, short, example string, f graphFlags, fn graphQuery) *cobra.Command {
	var q queryFlags
	var o query.GraphOpts
	var groupBy string
	cmd := &cobra.Command{
		Use: use, Short: short, Example: example,
		Long: short + ".\n\nAddresses: a stable key, repo:path#Name, path#Name in the current repo, or a bare name. " +
			"A name nothing has is answered with the nearest candidates, one several symbols have for the best of " +
			"them; --strict fails instead.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := groupByFile(groupBy, &o); err != nil {
				return err
			}
			return q.run(cmd, func(c *query.Ctx) (*output.Response, error) { return fn(c, args[0], o) })
		},
	}
	q.register(cmd)
	f.register(cmd, &q, &o, &groupBy)
	return cmd
}

// register adds the flags f selects to cmd.
func (f graphFlags) register(cmd *cobra.Command, q *queryFlags, o *query.GraphOpts, groupBy *string) {
	if f.sites {
		q.registerContext(cmd)
	}
	if f.deps {
		q.registerDeps(cmd)
	}
	o.Depth = 1
	fl := cmd.Flags()
	if f.depth {
		fl.IntVar(&o.Depth, "depth", 1, "how many edges to follow; above 1 the answer is a tree")
	}
	if f.noTests {
		fl.BoolVar(&o.NoTests, "no-tests", false, "leave out test code (every entry tagged test)")
	}
	if f.groupBy {
		fl.StringVar(groupBy, "group-by", "", "group the entries: file")
	}
	if f.kind {
		fl.StringVar(&o.Kind, "kind", "", "reference kind: type, instantiate or field")
	}
	if f.transitive {
		fl.BoolVar(&o.Transitive, "transitive", false, "also tests reaching the symbol through callers, up to depth 3")
	}
	if f.crossRepo {
		fl.BoolVar(&o.CrossRepo, "cross-repo", false, "follow imports into other repos of the workspace")
	}
}

func groupByFile(v string, o *query.GraphOpts) error {
	switch v {
	case "":
	case "file":
		o.GroupByFile = true
	default:
		return fmt.Errorf("--group-by %s: want file", v)
	}
	return nil
}

func newGraphCmds() []*cobra.Command {
	edges := graphFlags{noTests: true, groupBy: true, sites: true}
	walk := graphFlags{depth: true, noTests: true, groupBy: true, sites: true}
	return []*cobra.Command{
		newGraphCmd("callers <symbol>", "List the callers of a symbol, ambiguous calls included",
			"  codedoc callers Service.Lookup --depth 2\n  codedoc callers MemStore.Get --no-tests --group-by file",
			graphFlags{depth: true, noTests: true, groupBy: true, sites: true, deps: true}, query.Callers),
		newGraphCmd("callees <symbol>", "List the symbols a symbol calls", "  codedoc callees Service.Lookup",
			walk, query.Callees),
		newGraphCmd("refs <symbol>", "List the non-call references to a symbol", "  codedoc refs MemStore --kind instantiate",
			graphFlags{noTests: true, groupBy: true, kind: true, sites: true, deps: true}, query.Refs),
		newGraphCmd("impls <type>", "List the types implementing or extending a type", "  codedoc impls Store",
			edges, query.Impls),
		newGraphCmd("supertypes <type>", "List the types a type implements or extends", "  codedoc supertypes MemStore",
			graphFlags{groupBy: true, sites: true}, query.Supertypes),
		newGraphCmd("tests <symbol|module>", "List the tests that exercise a symbol or module",
			"  codedoc tests MemStore.Put\n  codedoc tests Service.Lookup --transitive", graphFlags{transitive: true, sites: true},
			query.Tests),
		newDepsCmd(),
		newGraphCmd("dependents <module>", "List the modules importing a module",
			"  codedoc dependents github.com/fixture/lib/store --cross-repo", graphFlags{depth: true, crossRepo: true},
			query.Dependents),
		newSiblingsCmd(),
	}
}

func newSiblingsCmd() *cobra.Command {
	var q queryFlags
	cmd := &cobra.Command{
		Use:   "siblings <symbol|file|dir|config:KEY>",
		Short: "List things of the same kind in the same place",
		Long: "List things of the same kind in the same place: other implementations or members for a symbol, " +
			"same-named or same-suffix files for a file, overlapping sibling directories for a directory, and the " +
			"other keys under the same parent for a config key (config:KEY, or a bare name no symbol has).",
		Example: "  codedoc siblings MemStore.Get\n  codedoc siblings go-lib:store/store_test.go\n  codedoc siblings config:db.host",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return q.run(cmd, func(c *query.Ctx) (*output.Response, error) { return query.Siblings(c, args[0]) })
		},
	}
	q.register(cmd)
	return cmd
}

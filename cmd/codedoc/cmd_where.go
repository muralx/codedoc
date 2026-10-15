package main

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/query"
)

func newWhereCmd() *cobra.Command {
	var q queryFlags
	var o query.WhereOpts
	cmd := &cobra.Command{
		Use:   "where <words>",
		Short: "Answer where something is done: the top locations with the lines that match",
		Example: "  codedoc where \"persist values by key\"\n  codedoc where retry a failed delivery -C 5\n" +
			"  codedoc where 'send a notification' --deps --limit 3",
		Long: "Where searches code, documents and their sections by keywords and, when an embedding model is " +
			"configured, by meaning, and answers with the top locations: each symbol or section with the lines " +
			"of its file that match, two lines around them (-C, -A, -B; --context symbol for the whole symbol). " +
			"A single strong hit is shown whole, as codedoc symbol shows it. --deps adds the packages the " +
			"workspace depends on.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			text := strings.Join(args, " ")
			return q.run(cmd, func(c *query.Ctx) (*output.Response, error) { return query.Where(c, text, o) })
		},
	}
	q.register(cmd)
	q.registerDeps(cmd)
	q.registerContext(cmd)
	cmd.Flags().IntVar(&o.Limit, "limit", 5, "locations shown; --offset continues")
	return cmd
}

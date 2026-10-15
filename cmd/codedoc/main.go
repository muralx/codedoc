// Command codedoc indexes a workspace of repositories and answers structural
// and documentation questions for coding agents.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime/debug"

	"github.com/spf13/cobra"

	"github.com/muralx/codedoc/internal/buildinfo"
	"github.com/muralx/codedoc/internal/query"
)

// version is set at build time with -ldflags "-X main.version=..." (make
// build, make install); see buildinfo.Resolve for other builds.
var version = "dev"

// init hands the version to the internal packages before any command
// runs.
func init() { buildinfo.Version = buildinfo.Resolve(version) }

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "codedoc",
		Short:         "Code knowledge index for coding agents",
		SilenceUsage:  true,
		SilenceErrors: true,
		// Version makes cobra add --version (and -v), which prints the
		// line of the version subcommand.
		Version: buildinfo.Version,
	}
	root.SetVersionTemplate(versionLine())
	root.AddCommand(newIndexCmd(), newStatusCmd(), newOutlineCmd(), newFindCmd(), newSymbolCmd(), newConfigCmd(), newDocCmd(), newGapsCmd(), newChangesCmd(), newHistoryCmd(), newAtCmd(), newOverviewCmd(), newSCIPCmd(), newMCPCmd(), newAutodocCmd(), newDaemonCmd())
	root.AddCommand(newLsCmd(), newLinesCmd(), newFeedbackCmd())
	root.AddCommand(newGraphCmds()...)
	root.AddCommand(newUnderstandCmds()...)
	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the version",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprint(cmd.OutOrStdout(), versionLine())
			return err
		},
	})
	return root
}

// versionLine is what codedoc version and codedoc --version print.
func versionLine() string { return "codedoc " + buildinfo.Version + "\n" }

func main() { os.Exit(execute(newRootCmd(), os.Stdout, os.Stderr)) }

// execute runs the command line on root and returns the exit status
// (spec section 13.1), after reporting the error (see reportError). An
// error cobra returns before the command itself runs (an unknown
// command or flag, a flag value that does not parse, a wrong argument
// count) is a usage error; a panic is reported as an internal error
// with its stack.
func execute(root *cobra.Command, stdout, stderr io.Writer) (code int) {
	defer func() {
		if v := recover(); v != nil {
			_, _ = fmt.Fprintf(stderr, "codedoc: internal error: %v\n%s", v, debug.Stack())
			code = exitInternal
		}
	}()
	ran := trackRun(root)
	cmd, err := root.ExecuteC()
	if err == nil {
		return 0
	}
	if !*ran {
		err = usageError{err}
	}
	reportError(stdout, stderr, cmd, err)
	return exitCode(err, strictRun(cmd))
}

// trackRun wraps the RunE of every command below root so that the
// value returned tells whether one of them started.
func trackRun(root *cobra.Command) *bool {
	ran := new(bool)
	var wrap func(*cobra.Command)
	wrap = func(cmd *cobra.Command) {
		if run := cmd.RunE; run != nil {
			cmd.RunE = func(cmd *cobra.Command, args []string) error {
				*ran = true
				return run(cmd, args)
			}
		}
		for _, sub := range cmd.Commands() {
			wrap(sub)
		}
	}
	wrap(root)
	return ran
}

// reportError writes err to stderr and, for a command run with --format
// json, also as a JSON object {"error": ..., "code": exit code} on a line
// of stdout, after any response the command printed there. An error
// whose failures the command already listed on stderr
// (query.ErrReported) adds no line there.
func reportError(stdout, stderr io.Writer, cmd *cobra.Command, err error) {
	if !errors.Is(err, query.ErrReported) {
		_, _ = fmt.Fprintln(stderr, "codedoc:", err)
	}
	if f := cmd.Flags().Lookup("format"); f != nil && f.Value.String() == "json" {
		_ = json.NewEncoder(stdout).Encode(struct {
			Error string `json:"error"`
			Code  int    `json:"code"`
		}{err.Error(), exitCode(err, strictRun(cmd))})
	}
}

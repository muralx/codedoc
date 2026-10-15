package main

import (
	"context"
	"io"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/muralx/codedoc/internal/daemon"
	"github.com/muralx/codedoc/internal/mcpserver"
)

func newMCPCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Serve the codedoc queries as MCP tools over stdio",
		Long: "Serve the codedoc MCP tools over stdin and stdout; logs go to stderr. The working directory selects the " +
			"workspace as for the CLI, and the server holds the session lock of each of its repos until it exits " +
			"(not under CODEDOC_HEADLESS), so scheduled autodoc runs skip them.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			log, err := daemon.NewLogger(cmd.ErrOrStderr(), "")
			if err != nil {
				return err
			}
			return mcpserver.Serve(cmd.Context(), &mcp.StdioTransport{}, mcpOptions(log))
		},
	}
}

// mcpOptions returns the server options that run each tool call through a
// fresh command tree, so MCP answers are the CLI's.
func mcpOptions(log mcpserver.Logger) mcpserver.Options {
	return mcpserver.Options{Exec: execCLI, Version: version, Log: log}
}

// execCLI runs the codedoc command line in process.
func execCLI(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	root := newRootCmd()
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetIn(stdin)
	return root.ExecuteContext(ctx)
}

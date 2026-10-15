// Package mcpserver serves the codedoc queries as MCP tools over stdio
// (spec section 13.2). Each tool call is translated to the arguments of the
// matching CLI command and run through an Executor, so the CLI and the MCP
// server give the same answers, and the server holds the session locks of
// its workspace while it runs (spec section 11.2).
package mcpserver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/query"
	"github.com/muralx/codedoc/internal/session"
)

// defaultBudget is the output budget of a tool call without a budget input.
const defaultBudget = 6000

// maxBudget is the largest output budget of a tool call.
const maxBudget = 60000

// Executor runs the CLI with args and stdin, writing its stdout and
// stderr to the given writers, and returns the command's error.
type Executor func(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error

// okText is the result of a write that succeeded: the CLI prints nothing
// then (efficiency spec section 6), and a tool result must have content.
const okText = "ok"

// Logger receives one line per event; the server logs to stderr only.
type Logger interface {
	Printf(format string, args ...any)
}

// Options configure a server.
type Options struct {
	Exec    Executor
	Version string
	Log     Logger
}

// Server is the codedoc MCP server: its tools (see registerTools) over
// one Executor.
type Server struct {
	mcp  *mcp.Server
	exec Executor
	log  Logger
	mu   sync.Mutex // serializes executions; the store is one connection per process
}

// New builds a server with its tools registered.
func New(opts Options) *Server {
	s := &Server{
		mcp:  mcp.NewServer(&mcp.Implementation{Name: "codedoc", Version: opts.Version}, nil),
		exec: opts.Exec,
		log:  opts.Log,
	}
	registerTools(s)
	return s
}

// Serve takes the session locks (unless CODEDOC_HEADLESS is set), runs
// the server on t until the client disconnects or ctx ends, and releases
// the locks.
func Serve(ctx context.Context, t mcp.Transport, opts Options) error {
	locks := lockSession(opts.Log)
	defer locks.release()
	return New(opts).mcp.Run(ctx, t)
}

// lockSession locks every repo of the workspace containing the working
// directory. Without a config or outside every workspace it takes none.
func lockSession(log Logger) sessionLocks {
	if session.Headless() {
		return nil
	}
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		log.Printf("no session locks: %v", err)
		return nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		log.Printf("no session locks: %v", err)
		return nil
	}
	ws, err := cfg.WorkspaceFor(cwd)
	if err != nil {
		log.Printf("no session locks (tools need the workspace input): %v", err)
		return nil
	}
	return takeLocks(filepath.Dir(cfg.DBPath(ws.Name)), ws.Name, ws.RepoNames(), log)
}

// input is implemented by every tool input: build sets the command words,
// flags and positional arguments; common returns the shared inputs.
type input interface {
	build(a *argv) error
	common() Common
}

// writer is implemented by the inputs of the tools that can write;
// writes reports whether this call does.
type writer interface {
	writes() bool
}

// budgeted is implemented by the inputs of the tools whose answer has a
// default budget of its own.
type budgeted interface {
	budget() int
}

// call runs one tool call. The nearest candidates of an address that
// matched nothing are the answer of the command and so a normal result;
// so are the one line of an address or path nothing can be offered for
// and the candidates --strict lists (see answers). Other failures are
// tool errors, and so is a panic, which is logged with its stack
// instead of ending the server. A write that succeeds without output
// answers okText; one that listed its failures itself
// (query.ErrReported) answers those lines alone. The lines of an answer
// that name the command for what a budget left out name the tool and
// its inputs instead (see toolHints).
func (s *Server) call(ctx context.Context, tool string, in input) (res *mcp.CallToolResult) {
	defer func() {
		if v := recover(); v != nil {
			s.log.Printf("%s: panic: %v\n%s", tool, v, debug.Stack())
			res = result(fmt.Sprintf("codedoc: internal error in %s: %v\n", tool, v), true)
		}
	}()
	var a argv
	if err := in.build(&a); err != nil {
		return result(err.Error(), true)
	}
	budget := defaultBudget
	if b, ok := in.(budgeted); ok {
		budget = b.budget()
	}
	in.common().flags(&a, budget)
	s.mu.Lock()
	defer s.mu.Unlock()
	var stdout, stderr bytes.Buffer
	err := s.exec(ctx, a.list(), strings.NewReader(a.stdin), &stdout, &stderr)
	if err == nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			s.log.Printf("%s: %s", tool, msg)
		}
		if w, ok := in.(writer); ok && w.writes() && stdout.Len() == 0 {
			return result(okText, false)
		}
		return result(toolHints(stdout.String(), in.common().Workspace), false)
	}
	if errors.Is(err, query.ErrReported) {
		return result(stdout.String()+stderr.String(), true)
	}
	text := toolHints(stdout.String(), in.common().Workspace) + stderr.String() + "codedoc: " + err.Error() + "\n" + workspaceHint(err)
	return result(text, !answers(err))
}

// answers reports whether the error of a command is an answer to what
// was asked rather than a failure of the call: nothing has that address,
// the path is outside the index (the line says why), or, under --strict,
// the address is ambiguous and the candidates were printed.
func answers(err error) bool {
	return errors.Is(err, query.ErrNotFound) || errors.Is(err, query.ErrNotIndexed) || errors.Is(err, query.ErrAmbiguous)
}

func result(text string, isErr bool) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}, IsError: isErr}
}

// workspaceHint explains, after an error saying the server's directory
// lies outside every workspace, that the workspace input selects one.
func workspaceHint(err error) string {
	msg := err.Error()
	if !strings.Contains(msg, "not inside any configured repository") && !strings.Contains(msg, "use --workspace") {
		return ""
	}
	hint := "codedoc: pass the workspace input to choose a workspace"
	if cfg, err := config.Load(config.DefaultPath()); err == nil && len(cfg.Names()) > 0 {
		hint += " (configured: " + strings.Join(cfg.Names(), ", ") + ")"
	}
	return hint + "\n"
}

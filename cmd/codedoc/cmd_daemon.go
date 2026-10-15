package main

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/daemon"
	"github.com/muralx/codedoc/internal/daemon/service"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/output"
)

func newDaemonCmd() *cobra.Command {
	var workspace string
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Keep the index fresh in the foreground: watch repos, follow branch switches, schedule SCIP and autodoc",
		Long: "Run in the foreground until SIGINT or SIGTERM: index changed files as they are saved and after branch " +
			"switches, embed what changed, serve status and freshness on the Unix socket ($XDG_RUNTIME_DIR/codedoc.sock), " +
			"and run SCIP and autodoc on the schedules of the workspace config. Where a workspace sets upkeep.enabled, " +
			"document what a branch changed once its repo is clean and quiet (codedoc status shows it per repo). " +
			"The config file is reloaded when it " +
			"changes. Only one daemon runs per data directory (a lock in it) and per socket. Logs go to stderr and to " +
			"$XDG_STATE_HOME/codedoc/daemon.log. deploy/systemd/codedoc.service runs it as a user service.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := config.DefaultPath()
			cfg, err := config.Load(path)
			if err != nil {
				return err
			}
			log, err := daemon.NewLogger(cmd.ErrOrStderr(), daemon.DefaultLogPath())
			if err != nil {
				return err
			}
			defer func() { _ = log.Close() }()
			svc, err := service.New(service.Options{Config: cfg, ConfigPath: path, Workspace: workspace, Log: log})
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return svc.Run(ctx)
		},
	}
	cmd.Flags().StringVar(&workspace, "workspace", "", "serve only this workspace (default: all)")
	return cmd
}

// daemonFresh asks the daemon whether a repo's index in database db is
// fresh: true only when it answers fresh with nothing pending, which it
// does only for the repo directory and database it keeps current.
func daemonFresh(workspace string, r *config.Repo, db string) bool {
	q := daemon.NewFreshQuery(workspace, r.Name, r.Path, db)
	q.Extractor = index.ExtractorVersion()
	reply, err := daemon.QueryFresh(daemon.SocketPath(), q)
	return err == nil && reply.Fresh && reply.Pending == 0
}

// withDaemon notes whether the daemon runs and, when it does, appends to
// each repo entry of a status section the daemon's queue, next scheduled
// runs, upkeep state, degraded state and last error, puts the upkeep
// state into the entry's JSON (extra.upkeep), and takes the daemon's
// freshness answer into states (in the order of the section's entries).
// It reports whether the daemon runs.
func withDaemon(resp *output.Response, workspace string, states []repoState) bool {
	st, err := daemon.QueryStatus(daemon.SocketPath())
	if err != nil {
		resp.Note = "daemon: not running"
		return false
	}
	resp.Note = "daemon: running"
	repos := map[string]daemon.RepoStatus{}
	for _, ws := range st.Workspaces {
		if ws.Name != workspace {
			continue
		}
		for _, r := range ws.Repos {
			repos[r.Name] = r
		}
	}
	for _, sec := range resp.Sections {
		for i, e := range sec.Entries {
			if r, ok := repos[e.Key]; ok {
				sec.Entries[i].Text += daemonText(r)
				setExtra(&sec.Entries[i], "upkeep", r.Upkeep)
				states[i].fresh = r.Fresh
			}
		}
	}
	return true
}

// upkeepNeedsDaemon says, on every repo entry of a status answer of a
// workspace with upkeep enabled and no daemon running, that nothing
// documents the repo meanwhile.
func upkeepNeedsDaemon(resp *output.Response) {
	resp.Note += "; upkeep needs the daemon (codedoc daemon): nothing is documented without it"
	for _, sec := range resp.Sections {
		for i := range sec.Entries {
			sec.Entries[i].Text += " upkeep:needs-daemon"
			setExtra(&sec.Entries[i], "upkeep", map[string]any{"enabled": true, "needs_daemon": true})
		}
	}
}

// setExtra sets a JSON-only field of an entry.
func setExtra(e *output.Entry, key string, value any) {
	if e.Extra == nil {
		e.Extra = map[string]any{}
	}
	e.Extra[key] = value
}

// daemonText describes a repo's daemon state.
func daemonText(r daemon.RepoStatus) string {
	text := fmt.Sprintf(" queue:%d", r.Queue)
	if r.Degraded != "" && !r.Missing {
		text += " degraded: " + r.Degraded
	}
	if !r.NextSCIP.IsZero() {
		text += " next-scip:" + r.NextSCIP.Local().Format(time.DateTime)
	}
	if !r.NextAutodoc.IsZero() {
		text += " next-autodoc:" + r.NextAutodoc.Local().Format(time.DateTime)
	}
	text += upkeepText(r.Upkeep)
	if r.LastError != "" {
		text += " last-error: " + firstLine(r.LastError)
	}
	if r.Upkeep.LastError != "" {
		text += " upkeep-error: " + firstLine(r.Upkeep.LastError)
	}
	return text
}

// upkeepText describes a repo's documentation upkeep: off, or on with
// what holds it back in parentheses (running when a run is going), the
// candidates the last check counted and when a run last finished.
func upkeepText(u daemon.UpkeepStatus) string {
	if !u.Enabled {
		return " upkeep:off"
	}
	text := " upkeep:on"
	switch {
	case u.Running:
		text = " upkeep:running"
	case u.CapReached:
		text += " (daily cap reached)"
	case u.Waiting != "":
		text += " (" + u.Waiting + ")"
	}
	text += fmt.Sprintf(" pending:%d", u.Pending)
	if !u.LastRun.IsZero() {
		text += " last-upkeep:" + u.LastRun.Local().Format(time.DateTime)
	}
	return text
}

// firstLine returns s up to its first line break.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

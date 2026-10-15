package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/query"
	"github.com/muralx/codedoc/internal/store"
)

// writeFlags is the flag the commands that write share (efficiency spec
// section 6): a write that succeeds prints nothing, its exit status is
// the result, and --verbose prints what was done. --format json prints
// the result too: a machine asked for it.
type writeFlags struct {
	verbose bool
}

func (w *writeFlags) register(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&w.verbose, "verbose", false, "print what was done (a write that succeeds prints nothing)")
}

// shows reports whether a write prints its result: with --verbose or
// --format json.
func (w *writeFlags) shows(format string) bool { return w.verbose || format == "json" }

// dest returns where a write prints its result: the command's stdout
// when it shows it, else nowhere.
func (w *writeFlags) dest(cmd *cobra.Command, format string) io.Writer {
	if w.shows(format) {
		return cmd.OutOrStdout()
	}
	return io.Discard
}

// reportFailures ends a command that goes on after a part of its work
// failed (a record of a doc set batch, a repo of an index run): it
// prints one line per failure on stderr and returns an error that makes
// the exit status non-zero and prints nothing more (query.ErrReported).
// what names the parts, total counts them. An error of the command
// itself comes first.
func reportFailures(stderr io.Writer, failed []string, total int, what string, err error) error {
	for _, line := range failed {
		if _, werr := fmt.Fprintln(stderr, line); werr != nil {
			return errors.Join(err, werr)
		}
	}
	if err != nil || len(failed) == 0 {
		return err
	}
	return fmt.Errorf("%d of %d %s failed: %w", len(failed), total, what, query.ErrReported)
}

// indexRepos indexes the target repos one by one. It returns the target
// narrowed to the repos it indexed, with their results, and one line per
// repo it could not index: "<repo>: <what>; <what to do>".
func indexRepos(s *store.Store, t target, opts index.Options) (target, []index.Result, []string) {
	var done []*config.Repo
	var results []index.Result
	var failed []string
	for _, r := range t.repos {
		res, err := index.Repo(s, r, opts)
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s: %s; fix that and run codedoc index --workspace %s --repo %s",
				r.Name, strings.Join(strings.Fields(err.Error()), " "), t.ws.Name, r.Name))
			continue
		}
		done, results = append(done, r), append(results, res)
	}
	t.repos = done
	return t, results, failed
}

// warnDeps prints, for a run that prints no summary, the one line that
// says its dependency sync left something to act on: the note of the
// summary, when it reports package versions that failed or manifests
// that could not be read.
func warnDeps(w io.Writer, note string) error {
	if !strings.Contains(note, "failed") && !strings.Contains(note, "unreadable manifest") {
		return nil
	}
	_, err := fmt.Fprintf(w, "warning: %s; see them with codedoc deps\n", note)
	return err
}

package scip

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/muralx/codedoc/internal/store"
)

// Outcome describes one language's indexer run on a repo.
type Outcome struct {
	// Lang is the language family ("go", "ts", "py", "java").
	Lang string
	// Text is a one-line description: the merge statistics, the missing
	// indexer, or why the run failed.
	Text string
	// Missing is set when the indexer binary is not installed.
	Missing *ErrIndexerMissing
	// Failed reports an indexer that ran but failed or wrote an
	// unreadable index.
	Failed bool
}

// RunRepo runs, in the repo's root, the SCIP indexer of each language
// family its indexed files use and merges every index at the repo's
// indexed commit, which therefore must be current. It returns one
// outcome per family, none when no family has an indexer. Indexer
// problems are outcomes; only store errors are returned. References to
// other repos use the mappings their last runs stored.
func (r Runner) RunRepo(ctx context.Context, s *store.Store, row store.Repo) ([]Outcome, error) {
	out, err := r.RunRepos(ctx, s, []store.Repo{row})
	if err != nil {
		return nil, err
	}
	return out[0], nil
}

// RunRepos is RunRepo over several repos, in two passes so the order of
// rows does not matter: every index is produced and parsed and its
// definitions mapped first, then every index is merged, so a reference
// from one repo to another finds that repo's mapping from this run. It
// returns the outcomes of each repo in rows order and records each
// repo's run (see LastRun).
func (r Runner) RunRepos(ctx context.Context, s *store.Store, rows []store.Repo) ([][]Outcome, error) {
	dir, err := os.MkdirTemp("", "codedoc-scip-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	out := make([][]Outcome, len(rows))
	runs := make([][]langRun, len(rows))
	for i, row := range rows {
		if out[i], runs[i], err = r.index(ctx, s, row, filepath.Join(dir, strconv.Itoa(i))); err != nil {
			return nil, err
		}
	}
	for i, row := range rows {
		for _, lr := range runs[i] {
			st, err := Merge(s, row, lr.ix, row.Commit)
			if err != nil {
				return nil, err
			}
			out[i][lr.at].Text = fmt.Sprintf("ran precise:+%d (refs added:%d) implements:%d mapped:%d files:%d "+
				"changed-since:%d dropped:%d", st.Precise, st.Added, st.Implements, st.Mapped, st.Files, st.Skipped, st.Dropped)
		}
		if len(out[i]) > 0 {
			if err := recordRun(s, row.Path, out[i], time.Now()); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// langRun is a parsed index waiting for its merge, with the index of its
// outcome.
type langRun struct {
	ix *Index
	at int
}

// index runs the indexer of each language family of a repo, writing the
// indexes under dir, and maps the definitions of those that parse. It
// returns one outcome per family and the indexes to merge.
func (r Runner) index(ctx context.Context, s *store.Store, row store.Repo, dir string) ([]Outcome, []langRun, error) {
	files, err := s.FilesByRepo(row.ID)
	if err != nil {
		return nil, nil, err
	}
	langs := make([]string, len(files))
	for i, f := range files {
		langs[i] = f.Lang
	}
	fams := Families(langs)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, nil, err
	}
	out := make([]Outcome, 0, len(fams))
	var runs []langRun
	for _, fam := range fams {
		o, ix := r.runParse(ctx, row, fam, filepath.Join(dir, fam+".scip"))
		if ix != nil {
			if err := MapDefinitions(s, row, ix, row.Commit); err != nil {
				return nil, nil, err
			}
			runs = append(runs, langRun{ix: ix, at: len(out)})
		}
		out = append(out, o)
	}
	return out, runs, nil
}

// runParse runs one family's indexer and parses its index; the index is
// nil when the indexer is missing or failed.
func (r Runner) runParse(ctx context.Context, row store.Repo, fam, file string) (Outcome, *Index) {
	o := Outcome{Lang: fam}
	err := r.RunLang(ctx, row.Path, fam, file)
	if errors.As(err, &o.Missing) {
		o.Text = "missing indexer " + o.Missing.Bin
		return o, nil
	}
	var ix *Index
	if err == nil {
		ix, err = ParseFile(file)
	}
	if err != nil {
		o.Failed, o.Text = true, "failed: "+err.Error()
		return o, nil
	}
	return o, ix
}

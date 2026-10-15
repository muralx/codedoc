package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/muralx/codedoc/internal/embed"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/store"
)

// embedRepos, when an embedder is configured, brings the vectors of the
// indexed repos up to date, adds to resp a section of one line counting
// the symbols and modules embedded (and, in JSON, the passage vectors
// written for the symbols) and checkpoints the database so vector scans
// read it memory mapped. The line is a section of its own, without a
// title: the index section counts repos. The packages the repos depend on are not embedded here: that
// would make every index wait for code that is not the user's (the daemon
// does it in the background, codedoc deps index --embed on request). A
// model that cannot load is reported once to warn and leaves search
// text-only.
func embedRepos(s *store.Store, t target, resp *output.Response, warn io.Writer) error {
	e := embed.Configured(t.cfg.Embed.ModelDir, warn)
	if e == nil {
		return nil
	}
	defer func() { _ = e.Close() }()
	var total index.VecResult
	for _, r := range t.repos {
		row, err := s.RepoByPath(r.Path)
		if err != nil {
			return err
		}
		res, err := index.EmbedVectors(s, e, store.VecScope{RepoID: row.ID}, 0)
		if err != nil {
			return fmt.Errorf("embed %s: %w", r.Name, err)
		}
		total.Symbols += res.Symbols
		total.Passages += res.Passages
		total.Modules += res.Modules
		if res.Unavailable {
			resp.Sections = append(resp.Sections, vectorsSection(output.Entry{Key: "vectors",
				Text: "embedding model unavailable; search is text-only"}))
			return nil
		}
	}
	resp.Sections = append(resp.Sections, vectorsSection(output.Entry{Key: "vectors",
		Text:  fmt.Sprintf("+%d symbols +%d modules", total.Symbols, total.Modules),
		Extra: map[string]any{"symbols": total.Symbols, "passages": total.Passages, "modules": total.Modules}}))
	return s.Checkpoint()
}

// vectorsSection is the section of the one line an index run says
// about its vectors.
func vectorsSection(e output.Entry) output.Section {
	return output.Section{Title: "vectors", NoTitle: true, Entries: []output.Entry{e}}
}

// withVectors adds vectors:N/M before the dirty count of a status entry
// of an indexed repo: its symbols and documented modules with up-to-date
// vectors over all that are embedded. Both count owners, not passages: a
// symbol with several passage vectors counts once. With an embedder configured, a vector of another size does
// not count; vectors a freshness check deferred show as the difference.
func withVectors(s *store.Store, t target, e output.Entry, repoID int64) (output.Entry, error) {
	if repoID == 0 {
		return e, nil
	}
	dims := 0
	if em := embed.Configured(t.cfg.Embed.ModelDir, nil); em != nil {
		dims = em.Dimensions()
	}
	n, documented, err := index.VectorCounts(s, repoID, dims)
	e.Text = strings.Replace(e.Text, " dirty:", fmt.Sprintf(" vectors:%d/%d dirty:", n, documented), 1)
	return e, err
}

package main

import (
	"fmt"
	"time"

	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/output"
	"github.com/muralx/codedoc/internal/query"
	"github.com/muralx/codedoc/internal/store"
)

// indexedStatus describes an indexed repo: its stored counts (files,
// symbols, config keys, unresolved config reads, parse errors), the
// live dirty count, when it was indexed, its SCIP state (see
// scipStatus) and, when the index is behind the working tree, by how
// much and what brings it up to date (see freshRemark).
func indexedStatus(s *store.Store, row store.Repo, f index.Freshness) (string, error) {
	maxFiles, err := query.FreshMax()
	if err != nil {
		return "", err
	}
	files, symbols, err := s.RepoStats(row.ID)
	if err != nil {
		return "", err
	}
	keys, unresolved, err := s.ConfigStats(row.ID)
	if err != nil {
		return "", err
	}
	parseErrors, err := s.ParseErrors(row.ID)
	if err != nil {
		return "", err
	}
	scipText, err := scipStatus(s, row)
	return fmt.Sprintf("%s files:%d symbols:%d config-keys:%d unresolved-config:%d parse-errors:%d dirty:%d indexed:%s %s%s",
		output.HeadRef(row.Branch, row.Commit), files, symbols, keys, unresolved, parseErrors, f.Dirty,
		time.Unix(row.IndexedAt, 0).UTC().Format(time.RFC3339), scipText, freshRemark(f, maxFiles)), err
}

// freshRemark says, for an index that is behind its working tree, why
// and what brings it up to date; "" when it is fresh. The reason is
// files waiting for re-extraction after an extractor update, files added
// or changed since it was indexed, else removed files or a moved head.
// Being behind is the normal state after an edit or a commit: the next
// query catches up by itself, unless more than maxFiles files changed
// (see query.FreshMax), which only codedoc index re-extracts.
func freshRemark(f index.Freshness, maxFiles int) string {
	var why string
	switch {
	case f.Fresh:
		return ""
	case f.Outdated > 0:
		why = fmt.Sprintf("extractor updated, %s to re-extract", files(f.Changed))
	case f.Changed > 0:
		why = files(f.Changed) + " changed since indexed"
	default:
		why = "the head moved or files were removed since indexed"
	}
	if f.Changed > maxFiles {
		return " (index behind: " + why + "; run codedoc index to bring it up to date)"
	}
	return " (index behind: " + why + "; the next query or codedoc index brings it up to date)"
}

// files counts files in words: "1 file", "2 files".
func files(n int) string {
	if n == 1 {
		return "1 file"
	}
	return fmt.Sprintf("%d files", n)
}

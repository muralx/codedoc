package query

import (
	"fmt"
	"strings"

	"github.com/muralx/codedoc/internal/config"
)

// staleNotes describes why the freshness check left repos of an answer
// behind their working tree: directories missing (answered from the
// last index), repos never indexed, files waiting for re-extraction
// after an extractor update, and plain changes over the limit.
func (c *Ctx) staleNotes(repos []*config.Repo) []string {
	var missing, unindexed []string
	outdated, changed, stale := 0, 0, false
	for _, r := range repos {
		switch f := c.Fresh[r.Name]; {
		case f.Fresh:
		case f.Missing:
			missing = append(missing, r.Name+" ("+r.Path+")")
		case f.Unindexed:
			unindexed = append(unindexed, r.Name)
		case f.Outdated > 0:
			outdated += f.Changed
		default:
			changed, stale = changed+f.Changed, true
		}
	}
	var notes []string
	if len(missing) > 0 {
		notes = append(notes, "directory missing, answered from the last index: "+strings.Join(missing, ", "))
	}
	if len(unindexed) > 0 {
		notes = append(notes, "not indexed: "+strings.Join(unindexed, ", ")+"; run codedoc index")
	}
	if outdated > 0 {
		notes = append(notes, fmt.Sprintf("extractor updated; %d %s to re-extract; run codedoc index", outdated,
			plural(outdated, "file", "files")))
	}
	if stale {
		notes = append(notes, fmt.Sprintf("index is stale (%d %s changed); run codedoc index", changed,
			plural(changed, "file", "files")))
	}
	return notes
}

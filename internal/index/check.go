package index

import (
	"os"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/store"
)

// Check reports, without writing, whether the index of one repo
// reflects its working tree, by the definition query headers use (see
// Ensure): it was indexed at the current head, no file was added,
// changed or removed since (content hashes compared, so a dirty file
// that was indexed is fresh), and no file was extracted with another
// extractor version. Changed counts the added and changed files,
// Outdated the files of another extractor version among them.
func Check(s *store.Store, r *config.Repo, opts Options) (Freshness, error) {
	if info, err := os.Stat(r.Path); err != nil || !info.IsDir() {
		return Freshness{Missing: true}, nil
	}
	f, st, cs, _, err := plan(s, r, opts)
	if err != nil {
		return f, err
	}
	upToDate := st.prev.ID != 0 && st.prev.Branch == st.head.Branch && st.prev.Commit == st.head.Commit
	f.Fresh = upToDate && f.Changed == 0 && len(cs.gone) == 0
	return f, nil
}

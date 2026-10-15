package index

import (
	"testing"

	"github.com/muralx/codedoc/internal/store"
)

// SetStoredExtractorVersion records version as the extractor version
// every file row of a repo was extracted with.
func SetStoredExtractorVersion(t *testing.T, s *store.Store, root, version string) {
	t.Helper()
	row, err := s.RepoByPath(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := s.FilesByRepo(row.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		f.ExtractedVersion = version
		if _, err := s.UpsertFile(f); err != nil {
			t.Fatal(err)
		}
	}
}

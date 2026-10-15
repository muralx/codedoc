package index

import "github.com/muralx/codedoc/internal/store"

// bulkTuning tunes bulk FTS mode (see suspendFTS); its zero value is
// the default, and tests lower minFiles or replace resume.
type bulkTuning struct {
	// minFiles is the fewest files a sync extracts in bulk FTS mode;
	// zero means batchSize.
	minFiles int
	// resume ends bulk FTS mode; nil means store.ResumeFTS. A test makes
	// it fail to simulate a process that dies before it.
	resume func(*store.Store) error
}

// minimum returns the fewest files a sync extracts in bulk FTS mode.
func (b bulkTuning) minimum() int {
	if b.minFiles > 0 {
		return b.minFiles
	}
	return batchSize
}

// resumeFTS ends bulk FTS mode.
func (b bulkTuning) resumeFTS(s *store.Store) error {
	if b.resume != nil {
		return b.resume(s)
	}
	return s.ResumeFTS()
}

// suspendFTS drops the FTS triggers for the sync (see store.SuspendFTS)
// when it extracts at least bulk.minimum() files and at least as many as
// the database holds besides the files it re-extracts and those of the
// repo a later round re-extracts (rows of another extractor version, see
// checkVersion): a first index, a change of most of the workspace, or a
// round of the catch-up after an extractor version bump. Rebuilding the text index once
// is then cheaper than indexing row by row; for a smaller change the
// rebuild of the rest of the database would cost more. It reports
// whether it suspended them: the caller then runs resumeFTS once the
// files are written.
func (sy *syncer) suspendFTS(write []Discovered, existing map[string]store.File) (bool, error) {
	n, known, later := 0, 0, len(sy.reseed)
	for _, d := range write {
		if sy.unchanged(d, existing) {
			continue
		}
		n++
		if _, ok := existing[d.Path]; ok {
			known++
		}
		if sy.reseed[d.Path] {
			later--
		}
	}
	if n < sy.bulk.minimum() {
		return false, nil
	}
	total, err := sy.s.FileTotal()
	if err != nil || n < total-known-later {
		return false, err
	}
	return true, sy.s.SuspendFTS()
}

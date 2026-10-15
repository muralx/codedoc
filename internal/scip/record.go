package scip

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"time"

	"github.com/muralx/codedoc/internal/store"
)

// RunRecord is the outcome of a repo's last SCIP run, kept in meta so
// status can tell a failed run from a merged one until a run succeeds.
type RunRecord struct {
	// At is when the run ended.
	At time.Time `json:"at"`
	// Failed is set when an indexer of the repo failed.
	Failed bool `json:"failed,omitempty"`
	// Missing lists the indexer binaries that were not installed.
	Missing []string `json:"missing,omitempty"`
}

// runKey is the meta key of a repo's last run record.
func runKey(root string) string { return "scip_run:" + root }

// recordRun stores the record of a repo's run from its outcomes.
func recordRun(s *store.Store, root string, outcomes []Outcome, at time.Time) (err error) {
	rec := RunRecord{At: at.UTC()}
	for _, o := range outcomes {
		rec.Failed = rec.Failed || o.Failed
		if o.Missing != nil {
			rec.Missing = append(rec.Missing, filepath.Base(o.Missing.Bin))
		}
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	unlock, err := s.WriteLock()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	return s.Batch(func(b *store.Batch) error { return b.SetMeta(runKey(root), string(raw)) })
}

// LastRun returns the record of the last SCIP run of the repo at root;
// ok is false before the first.
func LastRun(s *store.Store, root string) (rec RunRecord, ok bool, err error) {
	raw, ok, err := s.Meta(runKey(root))
	if err != nil || !ok {
		return rec, false, err
	}
	return rec, true, json.Unmarshal([]byte(raw), &rec)
}

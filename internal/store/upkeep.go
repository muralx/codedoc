package store

import (
	"database/sql"
	"errors"
)

// UpkeepBranch is the upkeep state of one branch of a repo.
type UpkeepBranch struct {
	// Commit is the last documented commit: upkeep looked at everything
	// up to it.
	Commit string
	// RanAt is when that was recorded, as Unix seconds.
	RanAt int64
}

// UpkeepBranch returns the upkeep state of a branch of a repo; ok is
// false when upkeep never finished there.
func (s *Store) UpkeepBranch(repoID int64, branch string) (u UpkeepBranch, ok bool, err error) {
	err = s.db.QueryRow(`SELECT "commit", ran_at FROM upkeep_branch WHERE repo_id = ? AND branch = ?`, repoID, branch).
		Scan(&u.Commit, &u.RanAt)
	if errors.Is(err, sql.ErrNoRows) {
		return UpkeepBranch{}, false, nil
	}
	return u, err == nil, err
}

// SetUpkeepBranch records commit as the last documented commit of a
// branch of a repo, at the Unix time at. Other branches keep theirs.
func (s *Store) SetUpkeepBranch(repoID int64, branch, commit string, at int64) error {
	_, err := s.db.Exec(`INSERT INTO upkeep_branch(repo_id, branch, "commit", ran_at) VALUES(?, ?, ?, ?)
  ON CONFLICT(repo_id, branch) DO UPDATE SET "commit" = excluded."commit", ran_at = excluded.ran_at`,
		repoID, branch, commit, at)
	return err
}

// UpkeepLastRun returns the Unix time upkeep last finished on any
// branch of a repo, 0 when it never did.
func (s *Store) UpkeepLastRun(repoID int64) (int64, error) {
	var at int64
	err := s.db.QueryRow(`SELECT COALESCE(MAX(ran_at), 0) FROM upkeep_branch WHERE repo_id = ?`, repoID).Scan(&at)
	return at, err
}

// UpkeepSpent returns what upkeep runs of the workspace spent on day
// (YYYY-MM-DD), in USD.
func (s *Store) UpkeepSpent(day string) (float64, error) {
	var usd float64
	err := s.db.QueryRow(`SELECT COALESCE(SUM(usd), 0) FROM upkeep_spend WHERE day = ?`, day).Scan(&usd)
	return usd, err
}

// AddUpkeepSpend adds usd to what upkeep spent on day (YYYY-MM-DD) and
// drops the days before it, which nothing reads any more.
func (s *Store) AddUpkeepSpend(day string, usd float64) error {
	return s.tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM upkeep_spend WHERE day < ?`, day); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO upkeep_spend(day, usd) VALUES(?, ?)
  ON CONFLICT(day) DO UPDATE SET usd = usd + excluded.usd`, day, usd)
		return err
	})
}

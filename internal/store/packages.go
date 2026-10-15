package store

// RepoByName returns the repo of a workspace with a name. In deps.db
// the name is a package id (deps spec section 3).
func (s *Store) RepoByName(workspace, name string) (Repo, error) {
	var r Repo
	err := s.db.QueryRow(`SELECT `+repoColumns+` FROM repo r JOIN workspace w ON w.id = r.workspace_id
  WHERE w.name = ? AND r.name = ?`, workspace, name).Scan(&r.ID, &r.Workspace, &r.Name, &r.Path, &r.VCS, &r.Branch,
		&r.Commit, &r.IndexedAt, &r.SCIPCommit)
	return r, notFound(err)
}

// Repos returns every repo row ordered by name.
func (s *Store) Repos() ([]Repo, error) {
	rows, err := s.db.Query(`SELECT ` + repoColumns + ` FROM repo r JOIN workspace w ON w.id = r.workspace_id
  ORDER BY r.name`)
	if err != nil {
		return nil, err
	}
	var out []Repo
	for rows.Next() {
		var r Repo
		if err := rows.Scan(&r.ID, &r.Workspace, &r.Name, &r.Path, &r.VCS, &r.Branch, &r.Commit, &r.IndexedAt,
			&r.SCIPCommit); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, r)
	}
	return out, closeRows(rows)
}

// SymbolCounts returns the number of symbols of every repo that has any,
// by repo id.
func (s *Store) SymbolCounts() (map[int64]int, error) {
	rows, err := s.db.Query(`SELECT f.repo_id, COUNT(*) FROM symbol s JOIN file f ON f.id = s.file_id GROUP BY f.repo_id`)
	if err != nil {
		return nil, err
	}
	out := map[int64]int{}
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out[id] = n
	}
	return out, closeRows(rows)
}

// SetRepoIndexedAt is Store.SetRepoIndexedAt inside the batch.
func (b *Batch) SetRepoIndexedAt(id, at int64) error {
	return requireOne(b.tx.Exec(`UPDATE repo SET indexed_at=? WHERE id=?`, at, id))
}

// DeleteRepo removes a repo row with everything that belongs to it:
// files and their symbols, modules, identities and dependency uses.
// Imports of other repos resolved to its modules become unresolved.
func (b *Batch) DeleteRepo(id int64) error {
	for _, q := range []string{
		`DELETE FROM file WHERE repo_id = ?`,
		`UPDATE import SET resolved_module_id = NULL WHERE resolved_module_id IN (SELECT id FROM module WHERE repo_id = ?)`,
		`UPDATE module SET parent_id = NULL WHERE repo_id = ?`,
		`DELETE FROM module WHERE repo_id = ?`,
	} {
		if _, err := b.tx.Exec(q, id); err != nil {
			return err
		}
	}
	return requireOne(b.tx.Exec(`DELETE FROM repo WHERE id = ?`, id))
}

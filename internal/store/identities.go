package store

// RepoIdentity is an identity a repo provides, from one manifest.
type RepoIdentity struct {
	Lang, Identity, Path string
}

// ReplaceRepoIdentities replaces the identities a repo provides.
func (s *Store) ReplaceRepoIdentities(repoID int64, ids []RepoIdentity) error {
	return s.Batch(func(b *Batch) error { return b.ReplaceRepoIdentities(repoID, ids) })
}

// ReplaceRepoIdentities is Store.ReplaceRepoIdentities inside the batch.
func (b *Batch) ReplaceRepoIdentities(repoID int64, ids []RepoIdentity) error {
	if _, err := b.tx.Exec(`DELETE FROM repo_identity WHERE repo_id = ?`, repoID); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := b.tx.Exec(`INSERT OR IGNORE INTO repo_identity(repo_id, lang, identity, path) VALUES(?, ?, ?, ?)`,
			repoID, id.Lang, id.Identity, id.Path); err != nil {
			return err
		}
	}
	return nil
}

// RepoIdentities returns the identities of a repo ordered by manifest path.
func (s *Store) RepoIdentities(repoID int64) ([]RepoIdentity, error) {
	rows, err := s.db.Query(`SELECT lang, identity, path FROM repo_identity WHERE repo_id = ? ORDER BY path, identity`, repoID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []RepoIdentity
	for rows.Next() {
		var id RepoIdentity
		if err := rows.Scan(&id.Lang, &id.Identity, &id.Path); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ReposProviding returns the repos that provide an identity, ordered by
// repo id. The store does not know the config order; callers that need it
// reorder the result.
func (s *Store) ReposProviding(lang, identity string) ([]Repo, error) {
	return reposProviding(s.db, lang, identity)
}

// ReposProviding is Store.ReposProviding inside the batch.
func (b *Batch) ReposProviding(lang, identity string) ([]Repo, error) {
	return reposProviding(b.tx, lang, identity)
}

func reposProviding(q rowsQuerier, lang, identity string) ([]Repo, error) {
	provs, err := queryProviders(q, `WHERE i.lang = ? AND i.identity = ? ORDER BY r.id`, lang, identity)
	out := make([]Repo, len(provs))
	for i, p := range provs {
		out[i] = p.Repo
	}
	return out, err
}

// Provider is a repo providing an identity.
type Provider struct {
	Lang, Identity string
	Repo           Repo
}

// Providers returns every identity of the workspace with the repo that
// provides it, ordered by identity and repo id.
func (b *Batch) Providers() ([]Provider, error) {
	return queryProviders(b.tx, `ORDER BY i.lang, i.identity, r.id`)
}

func queryProviders(q rowsQuerier, where string, args ...any) ([]Provider, error) {
	rows, err := q.Query(`SELECT i.lang, i.identity, `+repoColumns+` FROM repo_identity i
  JOIN repo r ON r.id = i.repo_id JOIN workspace w ON w.id = r.workspace_id `+where, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Provider
	for rows.Next() {
		var p Provider
		r := &p.Repo
		if err := rows.Scan(&p.Lang, &p.Identity, &r.ID, &r.Workspace, &r.Name, &r.Path, &r.VCS, &r.Branch,
			&r.Commit, &r.IndexedAt, &r.SCIPCommit); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SharedIdentity is an identity several repos provide.
type SharedIdentity struct {
	Lang, Identity string
	Repos          []string // repo names, by repo id
}

// DuplicateIdentities returns the identities provided by more than one
// repo, ordered by language and identity (spec 7.3).
func (s *Store) DuplicateIdentities() ([]SharedIdentity, error) {
	provs, err := queryProviders(s.db, `WHERE (i.lang, i.identity) IN (SELECT lang, identity FROM repo_identity
  GROUP BY lang, identity HAVING COUNT(*) > 1) ORDER BY i.lang, i.identity, r.id`)
	var out []SharedIdentity
	for _, p := range provs {
		if n := len(out); n > 0 && out[n-1].Lang == p.Lang && out[n-1].Identity == p.Identity {
			out[n-1].Repos = append(out[n-1].Repos, p.Repo.Name)
			continue
		}
		out = append(out, SharedIdentity{Lang: p.Lang, Identity: p.Identity, Repos: []string{p.Repo.Name}})
	}
	return out, err
}

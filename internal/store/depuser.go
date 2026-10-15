package store

// DepUser is one workspace database recorded in deps.db as a user of a
// package version (deps spec section 9).
type DepUser struct {
	// Package is the package id; WorkspaceDB the absolute path of the
	// workspace database; SeenAt when a sync last recorded the use, in
	// Unix seconds.
	Package, WorkspaceDB string
	SeenAt               int64
}

// Path returns the path of the database file.
func (s *Store) Path() string { return s.path }

// DepUsers returns the recorded users of every package version, or of
// those of one workspace database when workspaceDB is not empty,
// ordered by workspace database and package.
func (s *Store) DepUsers(workspaceDB string) ([]DepUser, error) {
	rows, err := s.db.Query(`SELECT package, workspace_db, seen_at FROM dep_user WHERE ? = '' OR workspace_db = ?
  ORDER BY workspace_db, package`, workspaceDB, workspaceDB)
	if err != nil {
		return nil, err
	}
	var out []DepUser
	for rows.Next() {
		var u DepUser
		if err := rows.Scan(&u.Package, &u.WorkspaceDB, &u.SeenAt); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, u)
	}
	return out, closeRows(rows)
}

// ReplaceDepUsers makes packages the package versions a workspace
// database uses, seen at the given time; none removes it as a user.
func (b *Batch) ReplaceDepUsers(workspaceDB string, packages []string, at int64) error {
	if _, err := b.tx.Exec(`DELETE FROM dep_user WHERE workspace_db = ?`, workspaceDB); err != nil {
		return err
	}
	for _, p := range packages {
		if _, err := (cached{b}).Exec(`INSERT OR REPLACE INTO dep_user(package, workspace_db, seen_at) VALUES(?, ?, ?)`,
			p, workspaceDB, at); err != nil {
			return err
		}
	}
	return nil
}

// UsedDepPackageIDs returns the distinct ids of the package versions
// the repos of the database resolve to, whatever their state but
// unused, sorted: what the workspace needs deps.db to keep.
func (s *Store) UsedDepPackageIDs() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT eco || '/' || package || '@' || version FROM dep_use
  WHERE version <> '' AND state <> ? ORDER BY 1`, DepUnused)
	if err != nil {
		return nil, err
	}
	return scanStrings(rows)
}

// ImportSite is one import specifier of a file that is not relative to
// the file.
type ImportSite struct {
	// Path is the repo-relative path of the importing file; Lang its
	// language; Raw the specifier as written.
	Path, Lang, Raw string
}

// ImportSites returns the distinct non-relative import specifiers of
// every file of a repo, ordered by path and specifier: what tells which
// third-party packages the code of the repo imports.
func (s *Store) ImportSites(repoID int64) ([]ImportSite, error) {
	rows, err := s.db.Query(`SELECT DISTINCT f.path, COALESCE(f.lang, ''), i.raw FROM import i
  JOIN file f ON f.id = i.file_id WHERE f.repo_id = ? AND i.raw NOT LIKE '.%' ORDER BY 1, 3`, repoID)
	if err != nil {
		return nil, err
	}
	var out []ImportSite
	for rows.Next() {
		var site ImportSite
		if err := rows.Scan(&site.Path, &site.Lang, &site.Raw); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, site)
	}
	return out, closeRows(rows)
}

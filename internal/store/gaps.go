package store

// GapSymbol is a symbol whose doc is not ok, with what gap ranking needs.
type GapSymbol struct {
	Symbol
	Generated bool
	// ChangedAt is the file's last commit time (Unix seconds), 0 unknown.
	ChangedAt int64
	// FanIn counts the distinct symbols with an edge into this one.
	FanIn int
	// ContainerKind and ContainerDoc describe the enclosing symbol;
	// empty at top level.
	ContainerKind, ContainerDoc string
}

// GapSymbols returns the non-test code symbols of a repo whose doc
// state is not ok, ordered by path and line.
func (s *Store) GapSymbols(repoID int64) ([]GapSymbol, error) {
	rows, err := s.db.Query(`SELECT `+symbolColumns+`, f.generated, COALESCE(f.last_commit_at,0),
  (SELECT COUNT(DISTINCT e.src_symbol_id) FROM edge e WHERE e.dst_symbol_id = s.id AND e.src_symbol_id <> s.id),
  COALESCE(c.kind,''), COALESCE(c.doc_state,'')`+symbolFrom+`LEFT JOIN symbol c ON c.id = s.container_id
  WHERE f.repo_id = ? AND s.is_test = 0 AND s.doc_state <> 'ok' AND `+codeKindSQL+symbolOrder, repoID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []GapSymbol
	for rows.Next() {
		var g GapSymbol
		sy := &g.Symbol
		if err := rows.Scan(&sy.ID, &sy.FileID, &sy.ModuleID, &sy.Kind, &sy.Name, &sy.Qualified, &sy.StableKey,
			&sy.ContainerID, &sy.StartLine, &sy.EndLine, &sy.StartByte, &sy.EndByte, &sy.SigStartLine, &sy.SigEndLine,
			&sy.Signature, &sy.Visibility, &sy.IsTest, &sy.BodyHash, &sy.DocText, &sy.DocHash, &sy.DocBodyHash,
			&sy.DocState, &sy.DocStartLine, &sy.DocEndLine, &sy.RepoID, &sy.Path, &sy.Lang, &sy.Value, &g.Generated,
			&g.ChangedAt, &g.FanIn, &g.ContainerKind, &g.ContainerDoc); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// GapModule is a module without a doc, with what gap ranking needs.
type GapModule struct {
	Module
	// FanIn counts the modules importing it.
	FanIn int
	// ChangedAt is the newest last commit time of the files below it.
	ChangedAt int64
}

// GapModules returns the modules of a repo without a doc, ordered by
// language and path.
func (s *Store) GapModules(repoID int64) ([]GapModule, error) {
	rows, err := s.db.Query(`SELECT `+moduleColumns+`,
  (SELECT COUNT(DISTINCT e.src_module_id) FROM module_edge e WHERE e.dst_module_id = module.id AND e.src_module_id <> module.id),
  (SELECT COALESCE(MAX(f.last_commit_at),0) FROM file f WHERE f.repo_id = module.repo_id
    AND (module.path = '.' OR f.path = module.path OR substr(f.path, 1, length(module.path) + 1) = module.path || '/'))
  FROM module WHERE repo_id = ? AND doc_state <> 'ok' ORDER BY lang, path`, repoID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []GapModule
	for rows.Next() {
		var g GapModule
		m := &g.Module
		if err := rows.Scan(&m.ID, &m.RepoID, &m.Path, &m.Lang, &m.Kind, &m.Identity, &m.ParentID, &m.DocText,
			&m.DocSource, &m.DocState, &m.Name, &g.FanIn, &g.ChangedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

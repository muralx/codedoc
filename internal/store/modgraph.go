package store

// ModuleByID returns the module with an id.
func (s *Store) ModuleByID(id int64) (Module, error) {
	return queryModule(s.db, `id = ?`, id)
}

// ModulesByIdentity returns the modules of the workspace with an
// identity, ordered by repo and path.
func (s *Store) ModulesByIdentity(identity string) ([]Module, error) {
	return queryModules(s.db, `WHERE identity = ? ORDER BY repo_id, path`, identity)
}

// ModuleWalkOpts control a module graph walk.
type ModuleWalkOpts struct {
	// Inbound walks to importers (dependents); otherwise to imported
	// modules (dependencies).
	Inbound bool
	// Depth is the most edges from the start; Limit caps the rows read.
	Depth, Limit int
	// RepoID, when not zero, keeps the walk inside that repo.
	RepoID int64
}

// ModuleWalk follows module edges from start breadth first with a
// recursive CTE, never revisiting a module on its own path and reading
// at most o.Limit rows. Step.EdgeID is zero: module edges have no id.
func (s *Store) ModuleWalk(start int64, o ModuleWalkOpts) ([]Step, error) {
	from, next := `src_module_id`, `dst_module_id`
	if o.Inbound {
		from, next = next, from
	}
	q := `WITH RECURSIVE up(id, depth, parent, path) AS (
  SELECT ?, 0, 0, ',' || ? || ','
  UNION ALL
  SELECT e.` + next + `, up.depth + 1, up.id, up.path || e.` + next + ` || ','
  FROM up JOIN module_edge e ON e.` + from + ` = up.id JOIN module m ON m.id = e.` + next + `
  WHERE up.depth < ? AND (? = 0 OR m.repo_id = ?) AND instr(up.path, ',' || e.` + next + ` || ',') = 0
  LIMIT ?)
SELECT id, depth, parent FROM up WHERE depth > 0`
	rows, err := s.db.Query(q, start, start, o.Depth, o.RepoID, o.RepoID, o.Limit+1)
	if err != nil {
		return nil, err
	}
	var out []Step
	for rows.Next() {
		var st Step
		if err := rows.Scan(&st.Node, &st.Depth, &st.Parent); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, st)
	}
	return out, closeRows(rows)
}

// ModuleEdgesTo returns the edges ending at a module ordered by source.
func (s *Store) ModuleEdgesTo(moduleID int64) ([]ModuleEdge, error) {
	rows, err := s.db.Query(`SELECT src_module_id, dst_module_id, kind, confidence FROM module_edge
  WHERE dst_module_id = ? ORDER BY src_module_id, kind`, moduleID)
	if err != nil {
		return nil, err
	}
	var out []ModuleEdge
	for rows.Next() {
		var e ModuleEdge
		if err := rows.Scan(&e.Src, &e.Dst, &e.Kind, &e.Confidence); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, e)
	}
	return out, closeRows(rows)
}

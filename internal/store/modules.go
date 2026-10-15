package store

// Module is a module row. ParentID is zero when NULL. The Doc fields
// are read only: UpsertModule leaves them alone and SetModuleDoc writes
// them; DocText and DocSource are empty when NULL.
type Module struct {
	ID, RepoID       int64
	Path, Lang, Kind string
	Identity         string
	// Name is the name the module's sources declare (a Go package
	// clause) when the index records it; "" otherwise.
	Name      string
	ParentID  int64
	DocText   string
	DocSource string
	DocState  string
}

const moduleColumns = `id, repo_id, path, lang, kind, identity, COALESCE(parent_id,0), COALESCE(doc_text,''),
  COALESCE(doc_source,''), doc_state, name`

// UpsertModule inserts or updates the module keyed by (repo, lang, path)
// and returns its id. ParentID 0 stores NULL.
func (s *Store) UpsertModule(m Module) (int64, error) { return upsertModule(s.db, m) }

// UpsertModule is Store.UpsertModule inside the batch.
func (b *Batch) UpsertModule(m Module) (int64, error) { return upsertModule(cached{b}, m) }

func upsertModule(q querier, m Module) (int64, error) {
	var id int64
	err := q.QueryRow(`INSERT INTO module(repo_id, path, lang, kind, identity, parent_id, name) VALUES(?, ?, ?, ?, ?, ?, ?)
  ON CONFLICT(repo_id, lang, path) DO UPDATE SET kind=excluded.kind, identity=excluded.identity, parent_id=excluded.parent_id,
  name=excluded.name RETURNING id`, m.RepoID, m.Path, m.Lang, m.Kind, m.Identity, nullID(m.ParentID), m.Name).Scan(&id)
	return id, err
}

// DeleteModules removes modules. Imports resolved to them become
// unresolved, child modules lose their parent and module edges go with
// them; symbols must no longer point at them.
func (b *Batch) DeleteModules(ids []int64) error {
	for _, id := range ids {
		for _, q := range []string{
			`UPDATE import SET resolved_module_id = NULL WHERE resolved_module_id = ?`,
			`UPDATE module SET parent_id = NULL WHERE parent_id = ?`,
			`DELETE FROM module WHERE id = ?`,
		} {
			if _, err := b.tx.Exec(q, id); err != nil {
				return err
			}
		}
	}
	return nil
}

// byIdentity orders the modules sharing an identity (copies of a package
// under testdata/, say) shallowest first, then by path.
const byIdentity = `repo_id = ? AND lang = ? AND identity = ?
  ORDER BY length(path) - length(replace(path, '/', '')), path LIMIT 1`

// ModuleByIdentity returns the module of a repo with an identity; when
// several share it, the shallowest.
func (s *Store) ModuleByIdentity(repoID int64, lang, identity string) (Module, error) {
	return queryModule(s.db, byIdentity, repoID, lang, identity)
}

// ModuleByIdentity is Store.ModuleByIdentity inside the batch.
func (b *Batch) ModuleByIdentity(repoID int64, lang, identity string) (Module, error) {
	return queryModule(cached{b}, byIdentity, repoID, lang, identity)
}

// ModuleByPath returns the module of a repo at a directory.
func (s *Store) ModuleByPath(repoID int64, lang, path string) (Module, error) {
	return queryModule(s.db, `repo_id = ? AND lang = ? AND path = ?`, repoID, lang, path)
}

// ModuleByPath is Store.ModuleByPath inside the batch.
func (b *Batch) ModuleByPath(repoID int64, lang, path string) (Module, error) {
	return queryModule(cached{b}, `repo_id = ? AND lang = ? AND path = ?`, repoID, lang, path)
}

// ModulesByRepo returns the modules of a repo ordered by language and path.
func (s *Store) ModulesByRepo(repoID int64) ([]Module, error) {
	return queryModules(s.db, `WHERE repo_id = ? ORDER BY lang, path`, repoID)
}

// Modules returns every module of the workspace ordered by repo,
// language and path.
func (b *Batch) Modules() ([]Module, error) {
	return queryModules(b.tx, `ORDER BY repo_id, lang, path`)
}

// AnyFile reports whether a repo holds a file at one of paths.
func (b *Batch) AnyFile(repoID int64, paths []string) (bool, error) {
	var n int
	err := b.tx.QueryRow(`SELECT COUNT(*) FROM file WHERE repo_id = ? AND path IN (`+placeholders(len(paths))+`)`,
		append([]any{repoID}, anys(paths)...)...).Scan(&n)
	return n > 0, err
}

func queryModules(q rowsQuerier, where string, args ...any) ([]Module, error) {
	rows, err := q.Query(`SELECT `+moduleColumns+` FROM module `+where, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Module
	for rows.Next() {
		m, err := scanModule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func queryModule(q querier, where string, args ...any) (Module, error) {
	m, err := scanModule(q.QueryRow(`SELECT `+moduleColumns+` FROM module WHERE `+where, args...))
	return m, notFound(err)
}

func scanModule(sc scanner) (Module, error) {
	var m Module
	err := sc.Scan(&m.ID, &m.RepoID, &m.Path, &m.Lang, &m.Kind, &m.Identity, &m.ParentID, &m.DocText, &m.DocSource,
		&m.DocState, &m.Name)
	return m, err
}

// SetModuleDoc stores a module's doc text, the repo-relative file it
// comes from and its doc state; empty text and source store NULL.
func (b *Batch) SetModuleDoc(id int64, text, source, state string) error {
	return requireOne(b.tx.Exec(`UPDATE module SET doc_text = ?, doc_source = ?, doc_state = ? WHERE id = ?`,
		nullStr(text), nullStr(source), state, id))
}

// ModuleByID is Store.ModuleByID inside the batch.
func (b *Batch) ModuleByID(id int64) (Module, error) {
	return queryModule(cached{b}, `id = ?`, id)
}

// ReposWithModule returns the repos holding a module of a language with
// an identity, ordered by repo id.
func (b *Batch) ReposWithModule(lang, identity string) ([]Repo, error) {
	rows, err := b.tx.Query(`SELECT `+repoColumns+` FROM repo r JOIN workspace w ON w.id = r.workspace_id
  WHERE r.id IN (SELECT repo_id FROM module WHERE lang = ? AND identity = ?) ORDER BY r.id`, lang, identity)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Repo
	for rows.Next() {
		var r Repo
		if err := rows.Scan(&r.ID, &r.Workspace, &r.Name, &r.Path, &r.VCS, &r.Branch, &r.Commit, &r.IndexedAt,
			&r.SCIPCommit); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

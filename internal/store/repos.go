package store

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"
)

// Repo is a repo row joined with its workspace name.
type Repo struct {
	ID         int64
	Workspace  string
	Name       string
	Path       string
	VCS        string
	Branch     string
	Commit     string
	IndexedAt  int64
	SCIPCommit string
}

// File is a file row.
type File struct {
	ID           int64
	RepoID       int64
	Path         string
	Lang         string
	Size         int64
	Mtime        int64
	Hash         string
	Lines        int
	Generated    bool
	ParseErrors  int
	LastCommit   string
	LastCommitAt int64
	// ExtractedVersion is the extractor version the file's rows were
	// extracted with; "" when unknown.
	ExtractedVersion string
	// Title is what codedoc ls prints after the file: a document's front
	// matter title or first heading, a code file's module doc sentence;
	// "" when it has none.
	Title string
}

const repoColumns = `r.id, w.name, r.name, r.path, r.vcs, COALESCE(r.branch,''), COALESCE(r."commit",''),
  COALESCE(r.indexed_at,0), COALESCE(r.scip_commit,'')`

const fileColumns = `id, repo_id, path, COALESCE(lang,''), size, mtime, hash, lines, generated, parse_errors,
  COALESCE(last_commit,''), COALESCE(last_commit_at,0), extracted_version, title`

// UpsertRepo inserts or updates the repo row keyed by path, creating the
// workspace row when needed, and returns the repo id. It writes the
// workspace, name, vcs, branch and commit; indexed_at and scip_commit are
// left to the indexing steps that own them. An empty VCS means git. A
// workspace repo name already indexed at another path follows the repo
// when that path is gone (the repo moved; file paths are relative to
// it); while both exist it fails naming codedoc index --rebuild.
func (s *Store) UpsertRepo(r Repo) (int64, error) {
	var id int64
	err := s.tx(func(tx *sql.Tx) error {
		var err error
		id, err = upsertRepo(tx, r)
		return err
	})
	return id, err
}

// UpsertRepo is Store.UpsertRepo inside the batch.
func (b *Batch) UpsertRepo(r Repo) (int64, error) { return upsertRepo(b.tx, r) }

func upsertRepo(tx *sql.Tx, r Repo) (int64, error) {
	if r.VCS == "" {
		r.VCS = "git"
	}
	if _, err := tx.Exec(`INSERT INTO workspace(name, created_at) VALUES(?, ?) ON CONFLICT(name) DO NOTHING`,
		r.Workspace, time.Now().Unix()); err != nil {
		return 0, err
	}
	if err := followMove(tx, r); err != nil {
		return 0, err
	}
	var id int64
	err := tx.QueryRow(`INSERT INTO repo(workspace_id, name, path, vcs, branch, "commit")
  VALUES((SELECT id FROM workspace WHERE name=?), ?, ?, ?, ?, ?)
  ON CONFLICT(path) DO UPDATE SET workspace_id=excluded.workspace_id, name=excluded.name,
    vcs=excluded.vcs, branch=excluded.branch, "commit"=excluded."commit"
  RETURNING id`, r.Workspace, r.Name, r.Path, r.VCS, r.Branch, r.Commit).Scan(&id)
	return id, err
}

// followMove moves the row of a workspace repo name indexed at another
// path to r.Path when that path no longer exists.
func followMove(tx *sql.Tx, r Repo) error {
	var id int64
	var old string
	err := tx.QueryRow(`SELECT r.id, r.path FROM repo r JOIN workspace w ON w.id = r.workspace_id
  WHERE w.name = ? AND r.name = ?`, r.Workspace, r.Name).Scan(&id, &old)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && old == r.Path) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, serr := os.Stat(old); !errors.Is(serr, fs.ErrNotExist) {
		return fmt.Errorf("repo %s of workspace %s is indexed at %s, now configured at %s: "+
			"run codedoc index --rebuild", r.Name, r.Workspace, old, r.Path)
	}
	_, err = tx.Exec(`UPDATE repo SET path = ? WHERE id = ?`, r.Path, id)
	return err
}

// RepoByPath returns the repo whose working tree is at path.
func (s *Store) RepoByPath(path string) (Repo, error) {
	return s.repoWhere(`r.path = ?`, path)
}

// RepoByID returns the repo row with an id.
func (s *Store) RepoByID(id int64) (Repo, error) {
	return s.repoWhere(`r.id = ?`, id)
}

func (s *Store) repoWhere(cond string, arg any) (Repo, error) {
	var r Repo
	err := s.db.QueryRow(`SELECT `+repoColumns+` FROM repo r JOIN workspace w ON w.id = r.workspace_id
  WHERE `+cond, arg).Scan(&r.ID, &r.Workspace, &r.Name, &r.Path, &r.VCS, &r.Branch, &r.Commit,
		&r.IndexedAt, &r.SCIPCommit)
	return r, notFound(err)
}

// UpdateRepoHead records the branch and commit the repo was indexed at.
func (s *Store) UpdateRepoHead(id int64, branch, commit string) error {
	return requireOne(s.db.Exec(`UPDATE repo SET branch=?, "commit"=? WHERE id=?`, branch, commit, id))
}

// SetRepoIndexedAt records when the repo was last indexed, as Unix seconds.
func (s *Store) SetRepoIndexedAt(id, at int64) error {
	return requireOne(s.db.Exec(`UPDATE repo SET indexed_at=? WHERE id=?`, at, id))
}

// RepoStats returns the number of file rows and of code symbol rows of
// a repo; documents and their sections are not counted as symbols.
func (s *Store) RepoStats(id int64) (files, symbols int, err error) {
	err = s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM file WHERE repo_id = ?),
  (SELECT COUNT(*) FROM symbol s JOIN file ON file.id = s.file_id WHERE file.repo_id = ? AND `+codeKindSQL+`)`, id, id).
		Scan(&files, &symbols)
	return files, symbols, err
}

// UpsertFile inserts or updates the file row keyed by (repo, path) and
// returns its id. Every column is overwritten from f.
func (s *Store) UpsertFile(f File) (int64, error) { return upsertFile(s.db, f) }

func upsertFile(q querier, f File) (int64, error) {
	var id int64
	err := q.QueryRow(`INSERT INTO file(repo_id, path, lang, size, mtime, hash, lines, generated,
    parse_errors, last_commit, last_commit_at, extracted_version, title)
  VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
  ON CONFLICT(repo_id, path) DO UPDATE SET lang=excluded.lang, size=excluded.size, mtime=excluded.mtime,
    hash=excluded.hash, lines=excluded.lines, generated=excluded.generated, parse_errors=excluded.parse_errors,
    last_commit=excluded.last_commit, last_commit_at=excluded.last_commit_at,
    extracted_version=excluded.extracted_version, title=excluded.title
  RETURNING id`, f.RepoID, f.Path, f.Lang, f.Size, f.Mtime, f.Hash, f.Lines, f.Generated,
		f.ParseErrors, f.LastCommit, f.LastCommitAt, f.ExtractedVersion, f.Title).Scan(&id)
	return id, err
}

// FilesByRepo returns every file row of a repo ordered by path.
func (s *Store) FilesByRepo(repoID int64) ([]File, error) {
	rows, err := s.db.Query(`SELECT `+fileColumns+` FROM file WHERE repo_id = ? ORDER BY path`, repoID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []File
	for rows.Next() {
		f, err := scanFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// FileIDsWhere returns the ids of the files, in every repo, whose
// repo-relative path match accepts, in id order.
func (s *Store) FileIDsWhere(match func(path string) bool) ([]int64, error) {
	rows, err := s.db.Query(`SELECT id, path FROM file ORDER BY id`)
	if err != nil {
		return nil, err
	}
	var out []int64
	for rows.Next() {
		var id int64
		var path string
		if err := rows.Scan(&id, &path); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if match(path) {
			out = append(out, id)
		}
	}
	return out, closeRows(rows)
}

// FileByPath returns the file row at a repo-relative path.
func (s *Store) FileByPath(repoID int64, path string) (File, error) {
	row := s.db.QueryRow(`SELECT `+fileColumns+` FROM file WHERE repo_id = ? AND path = ?`, repoID, path)
	f, err := scanFile(row)
	return f, notFound(err)
}

// FileByID returns the file row with an id.
func (s *Store) FileByID(id int64) (File, error) {
	f, err := scanFile(s.db.QueryRow(`SELECT `+fileColumns+` FROM file WHERE id = ?`, id))
	return f, notFound(err)
}

// DeleteFile removes a file row; symbols, edges and other rows that
// belong to the file go with it through ON DELETE CASCADE.
func (s *Store) DeleteFile(id int64) error { return deleteFile(s.db, id) }

func deleteFile(q querier, id int64) error {
	return requireOne(q.Exec(`DELETE FROM file WHERE id = ?`, id))
}

type scanner interface{ Scan(dest ...any) error }

func scanFile(sc scanner) (File, error) {
	var f File
	err := sc.Scan(&f.ID, &f.RepoID, &f.Path, &f.Lang, &f.Size, &f.Mtime, &f.Hash, &f.Lines, &f.Generated,
		&f.ParseErrors, &f.LastCommit, &f.LastCommitAt, &f.ExtractedVersion, &f.Title)
	return f, err
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

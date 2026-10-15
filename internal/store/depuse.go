package store

import (
	"database/sql"
	"strings"
)

// DepsDBKey is the meta key holding the path of the shared dependency
// database the uses of the workspace were last synced against: where
// reference resolution finds the packages of the indexed uses.
const DepsDBKey = "deps_db"

// States of a dependency use (deps spec section 4).
const (
	// DepIndexed: the package version has its rows in deps.db.
	DepIndexed = "indexed"
	// DepPending: the package is installed and waits to be indexed.
	DepPending = "pending"
	// DepMissing: the manifest declares the package but it is not
	// installed.
	DepMissing = "missing"
	// DepFailed: indexing the package failed; Error says why.
	DepFailed = "failed"
	// DepSkipped: the package is a workspace package, indexed as a repo.
	DepSkipped = "skipped"
	// DepUnused: the package is installed, but no source file of the
	// workspace imports it and deps.include does not name it, so it is
	// not indexed and no query sees it.
	DepUnused = "unused"
)

// DepUse is one package a manifest of a workspace repo resolves to.
type DepUse struct {
	RepoID int64
	// Manifest is the repo-relative path of the declaring manifest.
	Manifest string
	// Eco is the ecosystem (npm, go); Name the package name as the
	// manifest declares it, which import specifiers start with; Version
	// the installed version, "" when the package is missing. A source
	// that may change in place (a local directory, a git or patched
	// install) has a version starting with LocalVersionPrefix.
	Eco, Name, Version string
	// Package is the name the installed package gives itself when it
	// differs from Name (an npm alias); "" otherwise. It names the
	// package in deps.db.
	Package string
	// Declared is the version or range the manifest asks for.
	Declared string
	// Dev is true when the manifest declares the package for development
	// only (npm devDependencies and nowhere else).
	Dev   bool
	State string
	// Dir is the absolute directory the package is installed in; for a
	// missing Go module, where it is expected.
	Dir string
	// TypesDir is the directory of the @types package that supplies the
	// declarations of an npm package shipping none; "" otherwise.
	TypesDir string
	// Error is why a failed package could not be indexed.
	Error string
}

// LocalVersionPrefix starts the version of a package whose source may
// change in place; a short hash of its real directory follows.
const LocalVersionPrefix = "local-"

// PackageName returns the name that identifies the package in deps.db:
// Package when set, else Name.
func (u DepUse) PackageName() string {
	if u.Package != "" {
		return u.Package
	}
	return u.Name
}

// Mutable reports whether the use resolves to a source that may change
// in place (see LocalVersionPrefix).
func (u DepUse) Mutable() bool { return strings.HasPrefix(u.Version, LocalVersionPrefix) }

// PackageID returns eco/name@version, the name of the package's repo
// row in deps.db, or "" for a use without a version.
func (u DepUse) PackageID() string {
	if u.Version == "" {
		return ""
	}
	return u.Eco + "/" + u.PackageName() + "@" + u.Version
}

// Root returns the directory the paths of the package's file rows in
// deps.db are relative to: TypesDir when set, else Dir.
func (u DepUse) Root() string {
	if u.TypesDir != "" {
		return u.TypesDir
	}
	return u.Dir
}

// DepManifest is a file dependency discovery depends on: a manifest, a
// lock file or an install marker, with the stat and content hash it had
// when discovery last ran.
type DepManifest struct {
	Path        string
	Size, Mtime int64
	Hash        string
}

const depUseColumns = `repo_id, manifest_path, eco, name, version, declared, state, dir, types_dir, error, package, dev`

// DepUses returns the dependency uses of a repo, or of every repo when
// repoID is 0, ordered by repo, manifest, ecosystem and name.
func (s *Store) DepUses(repoID int64) ([]DepUse, error) {
	rows, err := s.db.Query(`SELECT `+depUseColumns+` FROM dep_use WHERE ? = 0 OR repo_id = ?
  ORDER BY repo_id, manifest_path, eco, name`, repoID, repoID)
	if err != nil {
		return nil, err
	}
	return scanDepUses(rows)
}

// ReplaceDepUses makes uses the dependency uses of a repo.
func (b *Batch) ReplaceDepUses(repoID int64, uses []DepUse) error {
	if _, err := b.tx.Exec(`DELETE FROM dep_use WHERE repo_id = ?`, repoID); err != nil {
		return err
	}
	for _, u := range uses {
		if _, err := (cached{b}).Exec(`INSERT OR REPLACE INTO dep_use(`+depUseColumns+`) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			repoID, u.Manifest, u.Eco, u.Name, u.Version, u.Declared, u.State, u.Dir, u.TypesDir, u.Error,
			u.PackageName(), u.Dev); err != nil {
			return err
		}
	}
	return nil
}

// SetDepState records the outcome of indexing a package version (name
// is its PackageName) on every use of it that is installed: pending,
// indexed or failed with errText.
func (b *Batch) SetDepState(eco, name, version, state, errText string) error {
	_, err := (cached{b}).Exec(`UPDATE dep_use SET state = ?, error = ? WHERE eco = ? AND package = ? AND version = ?
  AND state IN (?, ?, ?)`, state, errText, eco, name, version, DepPending, DepIndexed, DepFailed)
	return err
}

// SetDepUseState stores the state and error of one use: what the
// imports of the workspace make of it (unused, or pending, indexed or
// failed again).
func (b *Batch) SetDepUseState(u DepUse) error {
	_, err := (cached{b}).Exec(`UPDATE dep_use SET state = ?, error = ? WHERE repo_id = ? AND manifest_path = ?
  AND eco = ? AND name = ?`, u.State, u.Error, u.RepoID, u.Manifest, u.Eco, u.Name)
	return err
}

// ResetDepStates marks every indexed use pending again: deps.db lost its
// packages (codedoc deps prune --all).
func (b *Batch) ResetDepStates() error {
	_, err := b.tx.Exec(`UPDATE dep_use SET state = ? WHERE state = ?`, DepPending, DepIndexed)
	return err
}

// DepPackageIDs returns the distinct package ids in a state that a repo
// uses, or every repo when repoID is 0, sorted.
func (s *Store) DepPackageIDs(repoID int64, state string) ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT eco || '/' || package || '@' || version FROM dep_use
  WHERE state = ? AND version <> '' AND (? = 0 OR repo_id = ?) ORDER BY 1`, state, repoID, repoID)
	if err != nil {
		return nil, err
	}
	return scanStrings(rows)
}

// DepManifests returns the files discovery of a repo's dependencies last
// read, ordered by path.
func (s *Store) DepManifests(repoID int64) ([]DepManifest, error) {
	rows, err := s.db.Query(`SELECT path, size, mtime, hash FROM dep_manifest WHERE repo_id = ? ORDER BY path`, repoID)
	if err != nil {
		return nil, err
	}
	var out []DepManifest
	for rows.Next() {
		var m DepManifest
		if err := rows.Scan(&m.Path, &m.Size, &m.Mtime, &m.Hash); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, m)
	}
	return out, closeRows(rows)
}

// ReplaceDepManifests records the files discovery of a repo's
// dependencies read.
func (b *Batch) ReplaceDepManifests(repoID int64, files []DepManifest) error {
	if _, err := b.tx.Exec(`DELETE FROM dep_manifest WHERE repo_id = ?`, repoID); err != nil {
		return err
	}
	for _, m := range files {
		if _, err := (cached{b}).Exec(`INSERT OR REPLACE INTO dep_manifest(repo_id, path, size, mtime, hash)
  VALUES(?, ?, ?, ?, ?)`, repoID, m.Path, m.Size, m.Mtime, m.Hash); err != nil {
			return err
		}
	}
	return nil
}

// FilesNamed returns the paths of the files of a repo whose base name is
// one of names, sorted: the manifests of the indexed tree.
func (s *Store) FilesNamed(repoID int64, names ...string) ([]string, error) {
	conds := make([]string, len(names))
	args := []any{repoID}
	for i, n := range names {
		conds[i] = `path = ? OR path LIKE ? ESCAPE '\'`
		args = append(args, n, `%/`+escapeLike(n))
	}
	rows, err := s.db.Query(`SELECT path FROM file WHERE repo_id = ? AND (`+strings.Join(conds, " OR ")+`)
  ORDER BY path`, args...)
	if err != nil {
		return nil, err
	}
	return scanStrings(rows)
}

// escapeLike escapes the LIKE wildcards of s for ESCAPE '\'.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// IndexedDepUses returns the dependency uses of every repo whose package
// version is in deps.db, ordered like DepUses: the packages reference
// resolution may land in (deps spec section 6).
func (b *Batch) IndexedDepUses() ([]DepUse, error) {
	rows, err := b.tx.Query(`SELECT `+depUseColumns+` FROM dep_use WHERE state = ? AND version <> ''
  ORDER BY repo_id, manifest_path, eco, name`, DepIndexed)
	if err != nil {
		return nil, err
	}
	return scanDepUses(rows)
}

// scanDepUses reads dependency use rows and closes them.
func scanDepUses(rows *sql.Rows) ([]DepUse, error) {
	var out []DepUse
	for rows.Next() {
		var u DepUse
		if err := rows.Scan(&u.RepoID, &u.Manifest, &u.Eco, &u.Name, &u.Version, &u.Declared, &u.State, &u.Dir,
			&u.TypesDir, &u.Error, &u.Package, &u.Dev); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if u.Package == u.Name {
			u.Package = ""
		}
		out = append(out, u)
	}
	return out, closeRows(rows)
}

// FilesImporting returns the ids of the files of a repo with an import
// that resolved to no workspace module and names one of the packages:
// the specifier is a name itself or continues it with a slash (a
// subpath, a Go package of the module). These are the files whose
// references may land in those packages.
func (s *Store) FilesImporting(repoID int64, names []string) ([]int64, error) {
	seen := map[int64]bool{}
	var out []int64
	for _, n := range names {
		rows, err := s.db.Query(`SELECT DISTINCT i.file_id FROM import i JOIN file f ON f.id = i.file_id
  WHERE f.repo_id = ? AND i.resolved_module_id IS NULL AND (i.raw = ? OR i.raw LIKE ? ESCAPE '\')
  ORDER BY i.file_id`, repoID, n, escapeLike(n)+`/%`)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return nil, err
			}
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
		if err := closeRows(rows); err != nil {
			return nil, err
		}
	}
	return out, nil
}

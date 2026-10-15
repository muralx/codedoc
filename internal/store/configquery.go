package store

const configKeySelect = `SELECT k.id, k.repo_id, k.key, r.name FROM config_key k JOIN repo r ON r.id = k.repo_id `

// ConfigKeysByName returns the key rows named name, in every repo or in
// repo repoID when it is not 0, ordered by key and repo id.
func (s *Store) ConfigKeysByName(name string, repoID int64) ([]ConfigKey, error) {
	return queryConfigKeys(s.db, configKeySelect+`WHERE k.key = ? AND (? = 0 OR k.repo_id = ?) ORDER BY k.key, k.repo_id`,
		name, repoID, repoID)
}

// ConfigKeysMatching returns the key rows whose name matches a GLOB
// pattern, in every repo or in repo repoID when it is not 0, ordered by
// key and repo id.
func (s *Store) ConfigKeysMatching(glob string, repoID int64) ([]ConfigKey, error) {
	return queryConfigKeys(s.db, configKeySelect+`WHERE k.key GLOB ? AND (? = 0 OR k.repo_id = ?) ORDER BY k.key, k.repo_id`,
		glob, repoID, repoID)
}

// ConfigUse is a config_use row with its file's repo and path; SymbolID
// is zero for uses outside any symbol.
type ConfigUse struct {
	KeyID, FileID, SymbolID int64
	Line                    int
	Kind, Format, Preview   string
	RepoID                  int64
	Repo, Path              string
}

// ConfigUses returns the uses of the key rows ids, ordered by kind
// (defines, references, reads), repo, path and line.
func (s *Store) ConfigUses(ids []int64) ([]ConfigUse, error) {
	var out []ConfigUse
	err := inChunks(ids, func(chunk []any) error {
		rows, err := s.db.Query(`SELECT u.key_id, u.file_id, COALESCE(u.symbol_id,0), u.line, u.kind, u.format,
  COALESCE(u.value_preview,''), f.repo_id, r.name, f.path FROM config_use u JOIN file f ON f.id = u.file_id
  JOIN repo r ON r.id = f.repo_id WHERE u.key_id IN (`+placeholders(len(chunk))+`)
  ORDER BY CASE u.kind WHEN 'defines' THEN 0 WHEN 'references' THEN 1 ELSE 2 END, r.id, f.path, u.line`, chunk...)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var u ConfigUse
			if err := rows.Scan(&u.KeyID, &u.FileID, &u.SymbolID, &u.Line, &u.Kind, &u.Format, &u.Preview, &u.RepoID,
				&u.Repo, &u.Path); err != nil {
				return err
			}
			out = append(out, u)
		}
		return rows.Err()
	})
	return out, err
}

// ConfigStats returns the number of config keys a repo defines and the
// number of its accessor reads no defined key matched, reads through a
// constant whose value is known included; key-like literals that match
// no key are not counted, most of them are no config key.
func (s *Store) ConfigStats(repoID int64) (keys, unresolved int, err error) {
	err = s.db.QueryRow(`SELECT
  (SELECT COUNT(*) FROM config_key k WHERE k.repo_id = ?1 AND EXISTS (
    SELECT 1 FROM config_use u WHERE u.key_id = k.id AND u.kind = 'defines')),
  (SELECT COUNT(*) FROM reference r JOIN file f ON f.id = r.file_id WHERE f.repo_id = ?1 AND r.kind = 'config'
    AND (r.qualifier = ?2 OR r.config_key IS NOT NULL)
    AND NOT EXISTS (SELECT 1 FROM config_use u WHERE u.reference_id = r.id))`,
		repoID, ConfigAccessor).Scan(&keys, &unresolved)
	return keys, unresolved, err
}

// ConfigAccessor is the qualifier of a config candidate read through a
// recognized accessor such as os.Getenv, as opposed to a key-like string
// literal; it equals extract.ConfigAccessor. ConfigAccessorConst marks
// an accessor whose key argument is a constant name; it equals
// extract.ConfigAccessorConst.
const (
	ConfigAccessor      = "accessor"
	ConfigAccessorConst = "accessor-const"
)

// ConfigCandidate is a config candidate reference with its file's repo
// and path; SymbolID is zero for a candidate outside any symbol.
type ConfigCandidate struct {
	Name             string
	FileID, SymbolID int64
	Line             int
	RepoID           int64
	Repo, Path       string
}

// UnmatchedConfigReads returns the config candidates no defined key
// matched, the code reads of keys that no indexed config file defines,
// in every repo or in repo repoID when it is not 0, ordered by key name,
// repo, path and line. A candidate read through a constant counts under
// the constant's value, once the resolver recorded it. With glob false
// they are the candidates of key pattern, key-like literals included;
// with glob true those whose key matches the GLOB pattern and that read
// through an accessor, since a key-like literal is often no config key
// at all.
func (s *Store) UnmatchedConfigReads(pattern string, glob bool, repoID int64) ([]ConfigCandidate, error) {
	match, qualifier := `((r.name = ?1 AND r.qualifier <> ?3) OR r.config_key = ?1)`, ConfigAccessorConst
	if glob {
		match, qualifier = `((r.name GLOB ?1 AND r.qualifier = ?3) OR r.config_key GLOB ?1)`, ConfigAccessor
	}
	rows, err := s.db.Query(`SELECT COALESCE(r.config_key, r.name) AS key, r.file_id, COALESCE(r.symbol_id,0), r.line,
  f.repo_id, rp.name, f.path FROM reference r JOIN file f ON f.id = r.file_id JOIN repo rp ON rp.id = f.repo_id
  WHERE r.kind = 'config' AND `+match+` AND (?2 = 0 OR f.repo_id = ?2)
  AND NOT EXISTS (SELECT 1 FROM config_use u WHERE u.reference_id = r.id)
  ORDER BY key, rp.id, f.path, r.line`, pattern, repoID, qualifier)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ConfigCandidate
	for rows.Next() {
		var c ConfigCandidate
		if err := rows.Scan(&c.Name, &c.FileID, &c.SymbolID, &c.Line, &c.RepoID, &c.Repo, &c.Path); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

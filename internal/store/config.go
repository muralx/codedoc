package store

import "database/sql"

// Config use kinds (spec section 8).
const (
	ConfigDefines    = "defines"
	ConfigReferences = "references"
	ConfigReads      = "reads"
)

// ConfigEntry is one key occurrence in a config file: a definition with
// its value preview, or a reference (Preview empty).
type ConfigEntry struct {
	Key     string
	Line    int
	Preview string
}

// ReplaceFileConfig replaces the defines and references uses of a config
// file of a repo by defs and refs, creating the key rows they name
// (one per key name and repo).
func (b *Batch) ReplaceFileConfig(fileID, repoID int64, format string, defs, refs []ConfigEntry) error {
	if _, err := (cached{b}).Exec(`DELETE FROM config_use WHERE file_id = ? AND kind <> 'reads'`, fileID); err != nil {
		return err
	}
	for _, set := range []struct {
		kind    string
		entries []ConfigEntry
	}{{ConfigDefines, defs}, {ConfigReferences, refs}} {
		for _, e := range set.entries {
			var keyID int64
			if err := (cached{b}).QueryRow(`INSERT INTO config_key(repo_id, key) VALUES(?, ?)
  ON CONFLICT(repo_id, key) DO UPDATE SET key = excluded.key RETURNING id`, repoID, e.Key).Scan(&keyID); err != nil {
				return err
			}
			if _, err := (cached{b}).Exec(`INSERT INTO config_use(key_id, file_id, line, kind, format, value_preview)
  VALUES(?, ?, ?, ?, ?, ?)`, keyID, fileID, e.Line, set.kind, format, nullStr(e.Preview)); err != nil {
				return err
			}
		}
	}
	return nil
}

// ClearFileConfig deletes the defines and references uses of a file and
// returns the names of the keys it defined, sorted and distinct.
func (b *Batch) ClearFileConfig(fileID int64) ([]string, error) {
	rows, err := cached{b}.Query(`SELECT DISTINCT k.key FROM config_use u JOIN config_key k ON k.id = u.key_id
  WHERE u.file_id = ? AND u.kind = 'defines' ORDER BY k.key`, fileID)
	if err != nil {
		return nil, err
	}
	names, err := scanStrings(rows)
	if err != nil {
		return nil, err
	}
	_, err = cached{b}.Exec(`DELETE FROM config_use WHERE file_id = ? AND kind <> 'reads'`, fileID)
	return names, err
}

// DeleteOrphanConfigKeys deletes the key rows of a repo that no config
// file defines or references any more, with the reads matched to them.
func (b *Batch) DeleteOrphanConfigKeys(repoID int64) error {
	_, err := b.tx.Exec(`DELETE FROM config_key WHERE repo_id = ? AND NOT EXISTS (
  SELECT 1 FROM config_use u WHERE u.key_id = config_key.id AND u.kind <> 'reads')`, repoID)
	return err
}

// ConfigKey is a config_key row; Repo is the repo name (read only).
type ConfigKey struct {
	ID, RepoID int64
	Key, Repo  string
}

// DefinedConfigKeys returns the key rows with at least one defines use
// named one of names, or all of them when names is nil, ordered by id.
func (b *Batch) DefinedConfigKeys(names []string) ([]ConfigKey, error) {
	const q = `SELECT k.id, k.repo_id, k.key, r.name FROM config_key k JOIN repo r ON r.id = k.repo_id
  WHERE EXISTS (SELECT 1 FROM config_use u WHERE u.key_id = k.id AND u.kind = 'defines') `
	if names == nil {
		return queryConfigKeys(b.tx, q+`ORDER BY k.id`)
	}
	var out []ConfigKey
	err := inChunks(names, func(chunk []any) error {
		keys, err := queryConfigKeys(b.tx, q+`AND k.key IN (`+placeholders(len(chunk))+`) ORDER BY k.id`, chunk...)
		out = append(out, keys...)
		return err
	})
	return out, err
}

// ConfigRead is a code read of a config key matched from a reference.
type ConfigRead struct {
	KeyID, FileID, SymbolID, ReferenceID int64
	Line                                 int
	Format                               string
}

// InsertConfigRead stores one reads use.
func (b *Batch) InsertConfigRead(r ConfigRead) error {
	_, err := (cached{b}).Exec(`INSERT INTO config_use(key_id, file_id, symbol_id, line, kind, format, reference_id)
  VALUES(?, ?, ?, ?, 'reads', ?, ?)`, r.KeyID, r.FileID, nullID(r.SymbolID), r.Line, r.Format, r.ReferenceID)
	return err
}

// DeleteConfigReads deletes the reads matched from the references ids,
// or every reads use of the workspace when ids is nil.
func (b *Batch) DeleteConfigReads(ids []int64) error {
	if ids == nil {
		_, err := b.tx.Exec(`DELETE FROM config_use WHERE kind = 'reads'`)
		return err
	}
	return inChunks(ids, func(chunk []any) error {
		_, err := b.tx.Exec(`DELETE FROM config_use WHERE reference_id IN (`+placeholders(len(chunk))+`)`, chunk...)
		return err
	})
}

func queryConfigKeys(q rowsQuerier, query string, args ...any) ([]ConfigKey, error) {
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ConfigKey
	for rows.Next() {
		var k ConfigKey
		if err := rows.Scan(&k.ID, &k.RepoID, &k.Key, &k.Repo); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func scanStrings(rows *sql.Rows) ([]string, error) {
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

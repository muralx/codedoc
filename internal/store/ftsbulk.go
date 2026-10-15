package store

import (
	"database/sql"
	"errors"
	"os"
	"syscall"
)

// ftsRebuildKey is the meta key SuspendFTS sets and ResumeFTS clears:
// while it is set the FTS triggers may be missing and symbol_fts and
// module_fts out of date, so an interrupted bulk write is repaired by the
// next Open or WriteLock (see recoverFTS).
const ftsRebuildKey = "fts_rebuild_pending"

// ftsTriggers are the triggers that keep symbol_fts and module_fts in
// step with their content tables, by name; the statements are those of
// migration 1, made idempotent.
var ftsTriggers = []struct{ name, sql string }{
	{"symbol_ai", `CREATE TRIGGER IF NOT EXISTS symbol_ai AFTER INSERT ON symbol BEGIN
  INSERT INTO symbol_fts(rowid, name, qualified_name, signature, doc_text) VALUES (new.id, new.name, new.qualified_name, new.signature, new.doc_text); END`},
	{"symbol_ad", `CREATE TRIGGER IF NOT EXISTS symbol_ad AFTER DELETE ON symbol BEGIN
  INSERT INTO symbol_fts(symbol_fts, rowid, name, qualified_name, signature, doc_text) VALUES ('delete', old.id, old.name, old.qualified_name, old.signature, old.doc_text); END`},
	{"symbol_au", `CREATE TRIGGER IF NOT EXISTS symbol_au AFTER UPDATE ON symbol BEGIN
  INSERT INTO symbol_fts(symbol_fts, rowid, name, qualified_name, signature, doc_text) VALUES ('delete', old.id, old.name, old.qualified_name, old.signature, old.doc_text);
  INSERT INTO symbol_fts(rowid, name, qualified_name, signature, doc_text) VALUES (new.id, new.name, new.qualified_name, new.signature, new.doc_text); END`},
	{"module_ai", `CREATE TRIGGER IF NOT EXISTS module_ai AFTER INSERT ON module BEGIN
  INSERT INTO module_fts(rowid, path, identity, doc_text) VALUES (new.id, new.path, new.identity, new.doc_text); END`},
	{"module_ad", `CREATE TRIGGER IF NOT EXISTS module_ad AFTER DELETE ON module BEGIN
  INSERT INTO module_fts(module_fts, rowid, path, identity, doc_text) VALUES ('delete', old.id, old.path, old.identity, old.doc_text); END`},
	{"module_au", `CREATE TRIGGER IF NOT EXISTS module_au AFTER UPDATE ON module BEGIN
  INSERT INTO module_fts(module_fts, rowid, path, identity, doc_text) VALUES ('delete', old.id, old.path, old.identity, old.doc_text);
  INSERT INTO module_fts(rowid, path, identity, doc_text) VALUES (new.id, new.path, new.identity, new.doc_text); END`},
}

// SuspendFTS drops the FTS triggers for a bulk write: rows written
// until ResumeFTS do not touch symbol_fts and module_fts, which
// ResumeFTS then rebuilds in one pass. That is cheaper than indexing row
// by row when the write covers most of the database (a first index).
// The caller holds the write lock until ResumeFTS returns; text searches
// run meanwhile miss the rows written since the suspension. The
// fts_rebuild_pending meta key is set in the same transaction, so a
// process that dies before ResumeFTS leaves it for the next Open or
// WriteLock to finish.
func (s *Store) SuspendFTS() error {
	return s.tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT OR REPLACE INTO meta(key, value) VALUES(?, '1')`, ftsRebuildKey); err != nil {
			return err
		}
		for _, t := range ftsTriggers {
			if _, err := tx.Exec(`DROP TRIGGER IF EXISTS ` + t.name); err != nil {
				return err
			}
		}
		return nil
	})
}

// ResumeFTS rebuilds symbol_fts and module_fts from the symbol and
// module rows, recreates the FTS triggers and clears the
// fts_rebuild_pending key, in one transaction. It is idempotent.
func (s *Store) ResumeFTS() error {
	return s.tx(func(tx *sql.Tx) error {
		for _, q := range []string{`INSERT INTO symbol_fts(symbol_fts) VALUES('rebuild')`,
			`INSERT INTO module_fts(module_fts) VALUES('rebuild')`} {
			if _, err := tx.Exec(q); err != nil {
				return err
			}
		}
		for _, t := range ftsTriggers {
			if _, err := tx.Exec(t.sql); err != nil {
				return err
			}
		}
		_, err := tx.Exec(`DELETE FROM meta WHERE key = ?`, ftsRebuildKey)
		return err
	})
}

// ftsBroken reports whether a bulk write left the FTS tables to rebuild:
// the fts_rebuild_pending key is set or a trigger is missing.
func (s *Store) ftsBroken() (bool, error) {
	if _, pending, err := s.Meta(ftsRebuildKey); err != nil || pending {
		return pending, err
	}
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name IN
  ('symbol_ai', 'symbol_ad', 'symbol_au', 'module_ai', 'module_ad', 'module_au')`).Scan(&n)
	return n < len(ftsTriggers), err
}

// recoverFTS finishes the rebuild of an interrupted bulk write (see
// SuspendFTS). The caller holds the write lock.
func (s *Store) recoverFTS() error {
	broken, err := s.ftsBroken()
	if err != nil || !broken {
		return err
	}
	return s.ResumeFTS()
}

// recoverFTSOnOpen is recoverFTS for Open, which must not wait for a
// writer: it repairs only when the write lock is free. A writer holding
// it may be mid bulk write, and it or the next writer repairs.
func (s *Store) recoverFTSOnOpen() error {
	broken, err := s.ftsBroken()
	if err != nil || !broken {
		return err
	}
	f, err := os.OpenFile(s.path+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil
		}
		return err
	}
	return errors.Join(s.recoverFTS(), syscall.Flock(int(f.Fd()), syscall.LOCK_UN))
}

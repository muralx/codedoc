// Package store owns the SQLite schema and every read and write against it.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	_ "github.com/mattn/go-sqlite3" // sqlite driver
)

// ErrNotFound reports that a requested row does not exist.
var ErrNotFound = errors.New("store: not found")

// ErrNewerSchema reports a database whose schema a newer codedoc wrote.
var ErrNewerSchema = errors.New("database was created by a newer codedoc")

// Store is one open workspace database.
type Store struct {
	db   *sql.DB
	path string
}

// Open opens or creates the database at path and applies migrations.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	// Write transactions begin IMMEDIATE so a batch that reads before it
	// writes waits for other writers up front instead of failing to
	// upgrade its read snapshot. The page cache is 32 MiB (SQLite's
	// default is 2 MiB): the index B-trees a first index inserts into
	// stay cached, which made it ~10% faster on a 2,000-file repo; it is
	// allocated as pages are read, up to that bound. synchronous keeps
	// its default (FULL): NORMAL under WAL measured no faster here.
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000&_txlock=immediate&_cache_size=-32768")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, path: path}
	if err := s.initialize(); err != nil {
		_ = db.Close()
		return nil, openError(path, err)
	}
	return s, nil
}

// openError names the fix for a database that failed to open or
// migrate, on every command path: a binary built without FTS5 must be
// rebuilt with the tag; a database of a newer codedoc names its own
// fix; a busy database needs none; anything else (a failed or
// incompatible migration) is fixed by recreating it from source, since
// all data is derived (spec 15).
func openError(path string, err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "no such module: fts5"):
		return errors.New("built without FTS5 support: rebuild with -tags sqlite_fts5")
	case errors.Is(err, ErrNewerSchema):
		return err
	case strings.Contains(msg, "database is locked"), strings.Contains(msg, "database is busy"):
		return fmt.Errorf("open %s: %w", path, err)
	}
	return fmt.Errorf("open %s: %w (run codedoc index --rebuild to recreate it)", path, err)
}

// initialize connects and migrates under an exclusive flock on
// "<db path>.init". Processes opening a new database at the same moment
// would otherwise race to switch it to WAL mode, which fails at once
// with "database is locked" instead of waiting. The lock is held only
// while opening, never during index writes (see WriteLock). A bulk write
// that a dead process left unfinished is completed here when no writer
// holds the write lock (see SuspendFTS).
func (s *Store) initialize() error {
	f, err := os.OpenFile(s.path+".init", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	if err := s.migrate(); err != nil {
		return err
	}
	return s.recoverFTSOnOpen()
}

// Close releases the database.
func (s *Store) Close() error { return s.db.Close() }

// SchemaVersion returns the number of applied migrations.
func (s *Store) SchemaVersion() (int, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(v)
}

func (s *Store) migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS meta(key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		return err
	}
	current, err := s.SchemaVersion()
	if err != nil {
		return err
	}
	if current > len(migrations) {
		return fmt.Errorf("%w (schema %d > %d); upgrade codedoc or run codedoc index --rebuild",
			ErrNewerSchema, current, len(migrations))
	}
	for i := current; i < len(migrations); i++ {
		if err := s.tx(func(tx *sql.Tx) error {
			if _, err := tx.Exec(migrations[i]); err != nil {
				return fmt.Errorf("migration %d: %w", i+1, err)
			}
			_, err := tx.Exec(`INSERT OR REPLACE INTO meta(key,value) VALUES('schema_version',?)`, strconv.Itoa(i+1))
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}

// tx runs fn in a transaction. Because the pool holds one connection, fn
// must use only the *sql.Tx handle; touching s.db inside it deadlocks.
func (s *Store) tx(fn func(*sql.Tx) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// requireOne maps a write that touched no rows to ErrNotFound.
func requireOne(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

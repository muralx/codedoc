package store

import (
	"database/sql"
	"errors"
)

// querier is the subset of *sql.DB and *sql.Tx the row writers use.
type querier interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
}

// Batch applies writes inside one transaction; see Store.Batch.
type Batch struct {
	tx    *sql.Tx
	stmts map[string]*sql.Stmt
}

// Batch runs fn with a Batch whose writes commit together when fn returns
// nil and roll back when it returns an error. fn must write only through
// b: the store holds one connection, so calling other Store methods
// inside fn blocks.
func (s *Store) Batch(fn func(b *Batch) error) error {
	return s.tx(func(tx *sql.Tx) error { return fn(&Batch{tx: tx, stmts: map[string]*sql.Stmt{}}) })
}

// stmt returns the statement for a query prepared once per batch; the
// transaction closes it when it ends.
func (b *Batch) stmt(query string) (*sql.Stmt, error) {
	if st, ok := b.stmts[query]; ok {
		return st, nil
	}
	st, err := b.tx.Prepare(query)
	if err == nil {
		b.stmts[query] = st
	}
	return st, err
}

// cached is a querier that runs each query through a statement prepared
// once per batch.
type cached struct{ b *Batch }

func (c cached) Exec(query string, args ...any) (sql.Result, error) {
	st, err := c.b.stmt(query)
	if err != nil {
		return nil, err
	}
	return st.Exec(args...)
}

func (c cached) QueryRow(query string, args ...any) *sql.Row {
	st, err := c.b.stmt(query)
	if err != nil {
		return c.b.tx.QueryRow(query, args...)
	}
	return st.QueryRow(args...)
}

// Query runs a query through a statement prepared once per batch; the
// caller closes the rows before the batch runs the same query again.
func (c cached) Query(query string, args ...any) (*sql.Rows, error) {
	st, err := c.b.stmt(query)
	if err != nil {
		return nil, err
	}
	return st.Query(args...)
}

// UpsertFile is Store.UpsertFile inside the batch.
func (b *Batch) UpsertFile(f File) (int64, error) { return upsertFile(cached{b}, f) }

// Cleared is a symbol DeleteFiles deleted or ParkFileSymbols parked.
type Cleared struct{ Key, Name, Kind string }

// DeleteFiles deletes file rows and their symbols, one statement for
// each per maxParams files; edges and the other rows that belong to a
// file or a symbol go with them through ON DELETE CASCADE. It returns
// the stable keys, names and kinds of the deleted symbols by file id.
// The ids are distinct; one without a row is ErrNotFound.
func (b *Batch) DeleteFiles(ids []int64) (map[int64][]Cleared, error) {
	out := map[int64][]Cleared{}
	return out, inChunks(ids, func(chunk []any) error {
		in := placeholders(len(chunk))
		if err := b.deleteSymbols(in, chunk, out); err != nil {
			return err
		}
		res, err := cached{b}.Exec(`DELETE FROM file WHERE id IN (`+in+`)`, chunk...)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || int(n) != len(chunk) {
			return errors.Join(err, ErrNotFound)
		}
		return nil
	})
}

// deleteSymbols deletes the symbols of the files bound into the IN list
// in and adds them to out by file id.
func (b *Batch) deleteSymbols(in string, ids []any, out map[int64][]Cleared) error {
	rows, err := cached{b}.Query(`DELETE FROM symbol WHERE file_id IN (`+in+`)
  RETURNING file_id, stable_key, name, kind`, ids...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var c Cleared
		if err := rows.Scan(&id, &c.Key, &c.Name, &c.Kind); err != nil {
			return err
		}
		out[id] = append(out[id], c)
	}
	return rows.Err()
}

// KeyExists reports whether a symbol carries the stable key.
func (b *Batch) KeyExists(key string) (bool, error) {
	var n int
	err := cached{b}.QueryRow(`SELECT COUNT(*) FROM symbol WHERE stable_key = ?`, key).Scan(&n)
	return n > 0, err
}

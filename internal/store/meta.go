package store

import (
	"database/sql"
	"errors"
)

// Meta returns the value of a meta key; ok is false when it is not set.
func (s *Store) Meta(key string) (value string, ok bool, err error) {
	return meta(s.db, key)
}

// Meta is Store.Meta inside the batch.
func (b *Batch) Meta(key string) (value string, ok bool, err error) {
	return meta(b.tx, key)
}

func meta(q querier, key string) (string, bool, error) {
	var v string
	err := q.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

// SetMeta sets a meta key.
func (b *Batch) SetMeta(key, value string) error {
	_, err := b.tx.Exec(`INSERT OR REPLACE INTO meta(key, value) VALUES(?, ?)`, key, value)
	return err
}

// DeleteMeta removes a meta key.
func (b *Batch) DeleteMeta(key string) error {
	_, err := b.tx.Exec(`DELETE FROM meta WHERE key = ?`, key)
	return err
}

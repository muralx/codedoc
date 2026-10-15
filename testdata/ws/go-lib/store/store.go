// Package store holds the storage abstractions of the fixture library.
package store

import "errors"

// ErrMissing is returned when a key has no value.
var ErrMissing = errors.New("missing")

// Store persists string values by key.
type Store interface {
	// Get returns the value for key.
	Get(key string) (string, error)
	// Put stores value under key.
	Put(key, value string) error
}

// MemStore is an in-memory Store.
type MemStore struct {
	data map[string]string
}

// NewMemStore creates an empty MemStore.
func NewMemStore() *MemStore { return &MemStore{data: map[string]string{}} }

// Get implements Store.
func (m *MemStore) Get(key string) (string, error) {
	v, ok := m.data[key]
	if !ok {
		return "", ErrMissing
	}
	return v, nil
}

// Put implements Store.
func (m *MemStore) Put(key, value string) error {
	m.data[key] = value
	return nil
}

package store

// NullStore is a Store that keeps nothing.
type NullStore struct{}

func (NullStore) Get(key string) (string, error) { return "", ErrMissing }

func (NullStore) Put(key, value string) error { return nil }

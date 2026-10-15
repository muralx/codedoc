package store

import "strings"

// maxParams bounds the ids bound into one IN list, well below SQLite's
// host parameter limit.
const maxParams = 500

// placeholders returns n comma-separated question marks.
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// anys converts ids to query arguments.
func anys[T any](ids []T) []any {
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}

// closeRows closes rows and returns the first of the iteration and close
// errors.
func closeRows(rows interface {
	Close() error
	Err() error
}) error {
	err := rows.Err()
	if cerr := rows.Close(); err == nil {
		err = cerr
	}
	return err
}

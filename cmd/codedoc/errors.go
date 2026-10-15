package main

import (
	"errors"
	"io/fs"
	"os"
	"regexp"
	"syscall"

	"github.com/mattn/go-sqlite3"
	"github.com/spf13/cobra"

	"github.com/muralx/codedoc/internal/query"
)

// ErrNotFound and ErrAmbiguous are the query errors the exit statuses of
// spec section 13.1 tell apart.
var (
	ErrNotFound  = query.ErrNotFound
	ErrAmbiguous = query.ErrAmbiguous
)

// The exit statuses of the CLI (spec section 13.1). 0 is an answer,
// also the nearest candidates of an address that matched nothing.
const (
	// exitFailed: nothing at all could be offered (not found, a path
	// outside the index), a write failed, or any other failure that is
	// not one of the next three.
	exitFailed = 1
	// exitUsage: the command line is wrong: an unknown command or flag,
	// a bad argument count, an invalid flag value.
	exitUsage = 2
	// exitAmbiguous: under --strict, an address that names several
	// symbols; the candidates were printed.
	exitAmbiguous = 3
	// exitInternal: codedoc itself failed: the database, the file
	// system, a panic.
	exitInternal = 4
)

// usageError marks an error of the command line (exitUsage).
type usageError struct{ error }

// Unwrap returns the error that was marked.
func (e usageError) Unwrap() error { return e.error }

// usage returns a usage error with a message.
func usage(msg string) error { return usageError{errors.New(msg)} }

// flagFirst matches the message of an error about the value of a flag:
// by convention it starts with the flag and goes on about it ("--depth
// must be at least 1", "unknown --sort ...").
var flagFirst = regexp.MustCompile(`^(unknown )?--[a-z][a-z-]* `)

// isUsage reports whether err is an error of the command line: one
// marked as such (every error cobra returns before a command runs is,
// see execute), query.ErrUsage, or one whose message starts with a flag.
func isUsage(err error) bool {
	var u usageError
	return errors.As(err, &u) || errors.Is(err, query.ErrUsage) || flagFirst.MatchString(err.Error())
}

// isInternal reports whether err is a failure of codedoc itself rather
// than of what was asked: a database error, or a file system error
// other than a file that does not exist.
func isInternal(err error) bool {
	var db sqlite3.Error
	var path *fs.PathError
	var link *os.LinkError
	var sys *os.SyscallError
	var errno syscall.Errno
	switch {
	case errors.As(err, &db):
		return true
	case errors.Is(err, fs.ErrNotExist):
		return false
	}
	return errors.As(err, &path) || errors.As(err, &link) || errors.As(err, &sys) || errors.As(err, &errno)
}

// exitCode maps the error of a command to the exit status (spec section
// 13.1). Failures a command listed itself (query.ErrReported) and a
// failed doc set are plain failures whatever caused them; an ambiguous
// address is exitAmbiguous only when --strict asked for that error
// (strict, see strictRun).
func exitCode(err error, strict bool) int {
	var write *query.DocSetError
	switch {
	case errors.Is(err, query.ErrReported):
		return exitFailed
	case isUsage(err):
		return exitUsage
	case errors.Is(err, ErrAmbiguous) && strict:
		return exitAmbiguous
	case errors.As(err, &write), errors.Is(err, ErrNotFound), errors.Is(err, ErrAmbiguous):
		return exitFailed
	case isInternal(err):
		return exitInternal
	}
	return exitFailed
}

// strictRun reports whether cmd ran with --strict.
func strictRun(cmd *cobra.Command) bool {
	f := cmd.Flags().Lookup("strict")
	return f != nil && f.Value.String() == "true"
}

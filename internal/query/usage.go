package query

import (
	"errors"
	"fmt"
)

// ErrUsage reports an argument a command does not take as written (a
// line range that is none): the CLI exits with the status of a usage
// error (spec section 13.1). An error about the value of a flag needs
// no mark: its message starts with the flag.
var ErrUsage = errors.New("usage")

// usageError is an ErrUsage with its own message.
type usageError string

// Error returns the message.
func (e usageError) Error() string { return string(e) }

// Is reports whether target is ErrUsage.
func (e usageError) Is(target error) bool { return target == ErrUsage }

// usagef returns an ErrUsage with a formatted message.
func usagef(format string, args ...any) error { return usageError(fmt.Sprintf(format, args...)) }

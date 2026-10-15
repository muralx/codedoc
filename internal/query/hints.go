package query

import (
	"regexp"
	"strings"
)

// linesCmd is the command that prints a line range of a file, once the
// range follows it: what a body cut to the budget is continued with
// (output.Entry.Continue). addr names the file from any directory.
func linesCmd(addr string) string { return "codedoc lines " + shellQuote(addr) }

// shellSafe matches an argument a shell passes on as it is.
var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// shellQuote renders an argument of a command an answer suggests, so
// that the command works when pasted into a shell: as it is when it
// holds only characters no shell interprets, else in single quotes (a
// stable key may hold spaces, '>', '#' or '$').
func shellQuote(arg string) string {
	if shellSafe.MatchString(arg) {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}

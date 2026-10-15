package doc

import "strings"

// HTML comment markers.
const (
	commentOpen  = "<!--"
	commentClose = "-->"
)

// commentAfter reports whether an HTML comment is open at the end of a
// line, given whether one was open at its start. A comment closes at
// the first "-->" and may open again later on the same line; an opening
// marker inside an inline code span (`<!--`) opens nothing, and neither
// does one on a line indented as code.
func commentAfter(line string, open bool) bool {
	if _, ok := unindented(line); !ok && !open {
		return false
	}
	for line != "" {
		if open {
			_, rest, found := strings.Cut(line, commentClose)
			if !found {
				return true
			}
			open, line = false, rest
			continue
		}
		i := strings.Index(withoutCodeSpans(line), commentOpen)
		if i < 0 {
			return false
		}
		open, line = true, line[i+len(commentOpen):]
	}
	return open
}

// withoutCodeSpans blanks the inline code spans of a line (the text
// between two runs of backticks, the runs included), keeping every
// byte offset; a run without a closing one is left as it is.
func withoutCodeSpans(line string) string {
	out := []byte(line)
	for i := 0; i < len(out); {
		if out[i] != '`' {
			i++
			continue
		}
		n := runLength(line[i:], '`')
		end := closingRun(line[i+n:], n)
		if end < 0 {
			i += n
			continue
		}
		stop := i + n + end + n
		for j := i; j < stop; j++ {
			out[j] = ' '
		}
		i = stop
	}
	return string(out)
}

// runLength is the number of leading bytes of s equal to c.
func runLength(s string, c byte) int {
	n := 0
	for n < len(s) && s[n] == c {
		n++
	}
	return n
}

// closingRun is the offset in s of the first run of exactly n
// backticks, -1 when there is none.
func closingRun(s string, n int) int {
	for i := 0; i < len(s); {
		if s[i] != '`' {
			i++
			continue
		}
		run := runLength(s[i:], '`')
		if run == n {
			return i
		}
		i += run
	}
	return -1
}

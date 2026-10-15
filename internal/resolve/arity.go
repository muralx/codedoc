package resolve

import (
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/muralx/codedoc/internal/parse"
	"github.com/muralx/codedoc/internal/store"
)

// admits reports whether a candidate can take arity arguments. Anything
// but a function or method, a reference that is not a call (arity < 0),
// and a signature whose parameter list cannot be found admit any arity.
func admits(t store.Target, arity int) bool {
	if arity < 0 {
		return true
	}
	lo, hi, ok := argRange(t)
	return !ok || (arity >= lo && arity <= hi)
}

// argRange returns the least and most arguments a function or method
// takes; ok is false for other kinds and unparseable signatures. A Java
// constructor (<init>) is declared under its class name; a Python
// method's self or cls parameter is not an argument.
func argRange(t store.Target) (lo, hi int, ok bool) {
	if t.Kind != "function" && t.Kind != "method" {
		return 0, 0, false
	}
	name := t.Name
	if name == "<init>" {
		name = strings.TrimSuffix(t.Qualified, ".<init>")
		name = name[strings.LastIndex(name, ".")+1:]
	}
	params, ok := paramList(t.Signature, name)
	if !ok {
		return 0, 0, false
	}
	if t.Kind == "method" && len(params) > 0 && isSelf(params[0]) && parse.Family(t.Lang) == "py" {
		params = params[1:]
	}
	lo, hi = paramRange(params)
	return lo, hi, true
}

// SameArity reports whether two versions of a symbol accept the same
// argument counts, so references resolve to them alike.
func SameArity(a, b store.Target) bool {
	alo, ahi, aok := argRange(a)
	blo, bhi, bok := argRange(b)
	return aok == bok && alo == blo && ahi == bhi
}

// isSelf reports whether a Python parameter is self or cls.
func isSelf(p string) bool {
	name, _, _ := strings.Cut(p, ":")
	name = strings.TrimSpace(name)
	return name == "self" || name == "cls"
}

// paramRange returns the least and most arguments a parameter list
// accepts: the parameter list is the parenthesised list after the name
// (and its type parameters), so a Go receiver does not count. Variadic
// and rest parameters (Go and TS ..., Java T..., Python *args and
// **kwargs) lift the maximum; TS optional and defaulted (also Python)
// ones lower the minimum; a TS this parameter and the Python * and /
// markers are not arguments.
func paramRange(params []string) (lo, hi int) {
	variadic := false
	for _, p := range params {
		switch {
		case strings.HasPrefix(p, "this:") || strings.HasPrefix(p, "this ") || p == "*" || p == "/":
			continue
		case strings.Contains(p, "...") || strings.HasPrefix(p, "*"):
			variadic = true
		case strings.HasSuffix(strings.TrimSpace(strings.SplitN(p, ":", 2)[0]), "?") || strings.Contains(strings.ReplaceAll(p, "=>", ""), "="):
		default:
			lo++
		}
		hi++
	}
	if variadic {
		hi = math.MaxInt
	}
	return lo, hi
}

// paramList splits the parameter list that follows name in sig at its
// top-level commas.
func paramList(sig, name string) ([]string, bool) {
	start := nameParen(sig, name)
	if start < 0 {
		return nil, false
	}
	depth := 0
	var params []string
	from := start + 1
	for i := start; i < len(sig); i++ {
		depth += bracket(sig, i)
		switch {
		case depth == 0:
			return appendParam(params, sig[from:i]), true
		case sig[i] == ',' && depth == 1:
			params = appendParam(params, sig[from:i])
			from = i + 1
		}
	}
	return nil, false
}

// bracket returns +1 when sig[i] opens a bracket, -1 when it closes one,
// else 0. Angle brackets count as type arguments except in the arrows
// <-, -> and =>.
func bracket(sig string, i int) int {
	switch c := sig[i]; {
	case strings.IndexByte("([{", c) >= 0 || (c == '<' && !strings.HasPrefix(sig[i:], "<-")):
		return 1
	case strings.IndexByte(")]}", c) >= 0 || (c == '>' && i > 0 && sig[i-1] != '=' && sig[i-1] != '-'):
		return -1
	}
	return 0
}

func appendParam(params []string, p string) []string {
	if p = strings.TrimSpace(p); p != "" {
		params = append(params, p)
	}
	return params
}

// nameParen returns the index of the '(' opening the parameter list after
// the first whole-word occurrence of name followed by '(' or by type
// parameters, or -1.
func nameParen(sig, name string) int {
	for off := 0; ; {
		i := strings.Index(sig[off:], name)
		if i < 0 || name == "" {
			return -1
		}
		i += off
		off = i + len(name)
		if i > 0 && isIdent(sig[:i]) {
			continue
		}
		rest := sig[off:]
		if strings.HasPrefix(rest, "(") {
			return off
		}
		if strings.HasPrefix(rest, "<") || strings.HasPrefix(rest, "[") {
			if j := closing(rest); j > 0 && strings.HasPrefix(rest[j+1:], "(") {
				return off + j + 1
			}
		}
	}
}

// isIdent reports whether the text before a match ends in an identifier
// character, so the match is inside a longer word.
func isIdent(before string) bool {
	r, _ := utf8.DecodeLastRuneInString(before)
	return r == '_' || r == '$' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// closing returns the index of the bracket closing s[0] (one of <, [
// and the parenthesis), or -1.
func closing(s string) int {
	open, shut := s[0], byte(']')
	switch open {
	case '<':
		shut = '>'
	case '(':
		shut = ')'
	}
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case open:
			depth++
		case shut:
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

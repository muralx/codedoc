package cfg

import (
	"strconv"
	"strings"
)

// propertiesExtractor reads Java .properties files: key=value, key: value
// or key value, with backslash continuation lines joined.
type propertiesExtractor struct{}

// Extract implements Extractor. Properties files have no hard failures.
// References carry the physical line they appear on.
func (propertiesExtractor) Extract(_ string, src []byte) (*Result, error) {
	r := &Result{Format: Properties}
	lines := splitLines(string(src))
	for i := 0; i < len(lines); i++ {
		start := i
		logical := strings.TrimLeft(lines[i], " \t\f")
		if logical == "" || logical[0] == '#' || logical[0] == '!' {
			continue
		}
		for continues(logical) && i+1 < len(lines) {
			i++
			logical = logical[:len(logical)-1] + strings.TrimLeft(lines[i], " \t\f")
		}
		key, value := splitProperty(logical)
		if key == "" {
			continue
		}
		r.addDef(key, start+1, value)
		for j := start; j <= i; j++ {
			r.addPlaceholders(lines[j], j+1)
		}
	}
	return r, nil
}

// continues reports whether a line ends with an odd number of backslashes.
func continues(line string) bool {
	n := len(line) - len(strings.TrimRight(line, `\`))
	return n%2 == 1
}

// splitProperty splits a logical line at the first unescaped =, : or
// whitespace and unescapes the key and the value.
func splitProperty(line string) (key, value string) {
	i := 0
	for ; i < len(line); i++ {
		c := line[i]
		if c == '\\' {
			i++
			continue
		}
		if c == '=' || c == ':' || c == ' ' || c == '\t' || c == '\f' {
			break
		}
	}
	rest := strings.TrimLeft(line[min(i, len(line)):], " \t\f")
	if rest != "" && (rest[0] == '=' || rest[0] == ':') {
		rest = strings.TrimLeft(rest[1:], " \t\f")
	}
	return unescapeProperty(line[:min(i, len(line))]), unescapeProperty(rest)
}

// propertyEscapes maps the letters of the control-character escapes.
var propertyEscapes = map[byte]byte{'t': '\t', 'n': '\n', 'r': '\r', 'f': '\f'}

// unescapeProperty applies the java.util.Properties escapes: \uXXXX, \t,
// \n, \r and \f, and a backslash before any other character stands for
// that character. A malformed \u escape is kept as written.
func unescapeProperty(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 == len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch c := s[i]; {
		case c == 'u':
			if r, err := strconv.ParseUint(s[i+1:min(i+5, len(s))], 16, 32); err == nil && i+5 <= len(s) {
				b.WriteRune(rune(r))
				i += 4
			} else {
				b.WriteString(`\u`)
			}
		case propertyEscapes[c] != 0:
			b.WriteByte(propertyEscapes[c])
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

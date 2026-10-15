package cfg

import (
	"regexp"
	"strings"
)

// envLine matches KEY=value with an optional export prefix.
var envLine = regexp.MustCompile(`^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_.]*)\s*=\s*(.*)$`)

// envVar matches $X references.
var envVar = regexp.MustCompile(`\$([A-Za-z_][A-Za-z0-9_]*)`)

// envExtractor reads dotenv files: KEY=value lines, optionally prefixed by
// export, referring to other keys through ${X} and $X.
type envExtractor struct{}

// Extract implements Extractor. Dotenv files have no hard failures.
func (envExtractor) Extract(_ string, src []byte) (*Result, error) {
	r := &Result{Format: Env}
	for i, l := range splitLines(string(src)) {
		m := envLine.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		value, literal := envValue(m[2])
		r.addDef(m[1], i+1, value)
		if !literal {
			addEnvRefs(r, value, i+1)
		}
	}
	return r, nil
}

// envValue unquotes a raw value. Single-quoted values are literal (no
// expansion); unquoted values lose a trailing " # comment".
func envValue(raw string) (value string, literal bool) {
	raw = strings.TrimSpace(raw)
	if len(raw) >= 2 && (raw[0] == '"' || raw[0] == '\'') {
		if end := strings.IndexByte(raw[1:], raw[0]); end >= 0 {
			return raw[1 : end+1], raw[0] == '\''
		}
	}
	if c := strings.Index(raw, " #"); c >= 0 {
		raw = strings.TrimSpace(raw[:c])
	}
	return raw, false
}

// addEnvRefs records the ${X} placeholders and $X references of a value.
func addEnvRefs(r *Result, value string, line int) {
	r.addPlaceholders(value, line)
	for _, m := range envVar.FindAllStringSubmatch(value, -1) {
		r.addRef(m[1], line)
	}
}

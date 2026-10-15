// Package cfg extracts config keys from configuration and infrastructure
// files (spec 7.2): where each key gets a value and where a file refers to
// another key through a placeholder. Every extractor is pure: it sees only
// the path and the bytes of one file.
package cfg

import "github.com/muralx/codedoc/internal/extract"

// Format names, as stored on file rows and config_use rows.
const (
	YAML       = "yaml"
	JSON       = "json"
	XML        = "xml"
	Properties = "properties"
	Env        = "env"
	HCL        = "hcl"
	Shell      = "shell"
	Template   = "template"
)

// KeyDef is one place where a config key gets a value.
type KeyDef struct {
	Key     string // normalized key, case preserved
	Line    int    // 1-based
	Preview string // value text, single line, at most 80 bytes; *** for secrets
}

// KeyRef is one place where a config file refers to another key, e.g. ${X}.
type KeyRef struct {
	Key  string
	Line int // 1-based
}

// Result is everything extracted from one config file.
type Result struct {
	Format string // yaml, json, xml, properties, env, hcl, shell, template
	Defs   []KeyDef
	Refs   []KeyRef
}

// Extractor parses one config format. Implementations are pure. On a hard
// parse failure Extract returns the keys found before the failure together
// with the error, so the caller can both store them and count the error.
type Extractor interface {
	Extract(path string, src []byte) (*Result, error)
}

// addDef records a definition of key at line with the value text, masking
// the preview when the key names a secret.
func (r *Result) addDef(key string, line int, value string) {
	r.Defs = append(r.Defs, KeyDef{Key: key, Line: line, Preview: preview(key, value)})
}

// addRef records a reference to key at line.
func (r *Result) addRef(key string, line int) {
	r.Refs = append(r.Refs, KeyRef{Key: key, Line: line})
}

// addPlaceholders records the ${X} and ${X:default} placeholders of text,
// which starts at line; placeholders on later lines of text get their own
// line numbers.
func (r *Result) addPlaceholders(text string, line int) {
	for i, l := range splitLines(text) {
		for _, k := range extract.PlaceholderKeys(l) {
			r.addRef(k, line+i)
		}
	}
}

// Package stablekey builds and parses the stable keys that identify
// symbols across reindexing (spec section 5.1):
// repo:lang:module:qualified[#sighash][#pathhash].
package stablekey

import (
	"fmt"
	"regexp"
	"strings"
)

// Key is a parsed stable key. SigHash holds the whole hash suffix: a
// signature hash, a collision path hash, or, for an overload-capable
// symbol that collided, both joined by '#'.
type Key struct {
	Repo, Lang, Module, Qualified, SigHash string
}

// knownLangs are the language family segments a stable key may carry.
// "doc" is the family of documents and their sections, whose module
// segment is the file path.
var knownLangs = map[string]bool{"go": true, "ts": true, "py": true, "java": true, docLang: true}

// docLang is the language segment of document and section keys.
const docLang = "doc"

// pathHashPattern is a collision suffix: four lowercase hex digits, or
// the fallbacks of eight digits with an optional -N ordinal.
const pathHashPattern = `[0-9a-f]{4}|[0-9a-f]{8}(?:-[0-9]+)?`

// sigHashSuffix matches the optional hash suffix at the end of a key: a
// four digit hash or a collision suffix, optionally preceded by a four
// digit signature hash (Java overloads that also collided), not preceded
// by '.', so a '#' that belongs to the qualified name (a TypeScript
// private member such as Base.#cafe, or a test title) is kept.
var sigHashSuffix = regexp.MustCompile(`[^.]#((?:[0-9a-f]{4}#)?(?:` + pathHashPattern + `))$`)

// pathHashSuffix matches a collision suffix; sigPathSuffix one that
// follows a signature hash, as group 1.
var (
	pathHashSuffix = regexp.MustCompile(`[^.]#(?:` + pathHashPattern + `)$`)
	sigPathSuffix  = regexp.MustCompile(`[^.]#[0-9a-f]{4}(#(?:` + pathHashPattern + `))$`)
)

// KnownLang reports whether s is a language family used in stable keys.
func KnownLang(s string) bool { return knownLangs[s] }

// New builds a key, writing ':' in the repo, language and module
// identity as '/' so the key stays parseable (Maven group:artifact,
// Rust crate::path).
func New(repo, lang, module, qualified, sigHash string) Key {
	clean := func(s string) string { return strings.ReplaceAll(s, ":", "/") }
	return Key{Repo: clean(repo), Lang: clean(lang), Module: clean(module), Qualified: qualified, SigHash: sigHash}
}

// String renders the key, appending #SigHash when set.
func (k Key) String() string {
	s := k.Repo + ":" + k.Lang + ":" + k.Module + ":" + k.Qualified
	if k.SigHash != "" {
		s += "#" + k.SigHash
	}
	return s
}

// Parse splits off an optional #hash suffix, then splits the rest on its
// first three colons; the remainder is the qualified name. A document
// key (language doc) carries no hash, since a heading may end like one
// ("Issue #1234"), and its qualified name is empty for the document
// itself.
func Parse(s string) (Key, error) {
	var k Key
	rest := s
	isDoc := false
	if head := strings.SplitN(s, ":", 3); len(head) == 3 {
		isDoc = head[1] == docLang
	}
	if m := sigHashSuffix.FindStringSubmatchIndex(s); m != nil && !isDoc {
		k.SigHash, rest = s[m[2]:m[3]], s[:m[0]+1]
	}
	parts := strings.SplitN(rest, ":", 4)
	if len(parts) != 4 || (parts[3] == "" && !isDoc) {
		return Key{}, fmt.Errorf("invalid stable key %q", s)
	}
	k.Repo, k.Lang, k.Module, k.Qualified = parts[0], parts[1], parts[2], parts[3]
	return k, nil
}

// IsKey reports whether s parses as a stable key whose language segment
// is a known family: the test that tells a key from the other address
// forms.
func IsKey(s string) bool {
	k, err := Parse(s)
	return err == nil && KnownLang(k.Lang)
}

// Repo returns the repo segment of key, or "" when key is no stable key.
func Repo(key string) string {
	k, _ := Parse(key)
	return k.Repo
}

// Module returns the module identity of key, or "" when key is no stable
// key.
func Module(key string) string {
	k, _ := Parse(key)
	return k.Module
}

// Base returns a key without its collision suffix. sig tells that the
// key carries a signature hash: it belongs to the base, and only a second
// suffix after it is a collision suffix.
func Base(key string, sig bool) string {
	if sig {
		if m := sigPathSuffix.FindStringSubmatchIndex(key); m != nil {
			return key[:m[2]]
		}
		return key
	}
	if m := pathHashSuffix.FindStringIndex(key); m != nil {
		return key[:m[0]+1]
	}
	return key
}

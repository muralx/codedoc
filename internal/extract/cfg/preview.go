package cfg

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxPreview is the longest value preview in bytes.
const maxPreview = 80

// masked replaces the preview of a secret key.
const masked = "***"

// secretWords are the key words that mark a value as secret wherever they
// appear in the key.
var secretWords = map[string]bool{
	"password": true, "passwd": true, "secret": true, "token": true, "credentials": true,
	"apikey": true, "privatekey": true,
}

// keyQualifiers are the words that make a following "key" a secret: the
// pair api key and private key anywhere, the others only when key is the
// last word (signing.key, but not spring.kafka.key-serializer).
var keyQualifiers = map[string]bool{
	"api": true, "private": true, "secret": true, "access": true, "signing": true, "encryption": true,
}

// preview returns the value as one trimmed line of at most maxPreview bytes,
// cut on a rune boundary, or masked when key names a secret.
func preview(key, value string) string {
	if isSecret(key) {
		return masked
	}
	v := strings.Join(strings.Fields(value), " ")
	if len(v) <= maxPreview {
		return v
	}
	cut := maxPreview
	for cut > 0 && !utf8.RuneStart(v[cut]) {
		cut--
	}
	return v[:cut]
}

// isSecret reports whether key names a secret, ignoring case. The key is
// split into words on . _ - @ and camelCase boundaries; it is a secret when
// a word is a secret word, when api or private precedes key anywhere
// (api_key.value), or when the last word is key and the word before it is
// a key qualifier (jwt.signing-key).
func isSecret(key string) bool {
	words := keySegments(key)
	for i, w := range words {
		w = strings.ToLower(w)
		words[i] = w
		if secretWords[w] {
			return true
		}
		if i > 0 && w == "key" && (words[i-1] == "api" || words[i-1] == "private") {
			return true
		}
	}
	n := len(words)
	return n >= 2 && words[n-1] == "key" && keyQualifiers[words[n-2]]
}

// keySegments splits key on separators and lower-to-upper case changes:
// "db.apiKey" yields db, api, Key.
func keySegments(key string) []string {
	var segs []string
	start := 0
	runes := []rune(key)
	for i, r := range runes {
		switch {
		case r == '.' || r == '_' || r == '-' || r == '@':
			segs = append(segs, string(runes[start:i]))
			start = i + 1
		case i > start && unicode.IsUpper(r) && unicode.IsLower(runes[i-1]):
			segs = append(segs, string(runes[start:i]))
			start = i
		}
	}
	return append(segs, string(runes[start:]))
}

// splitLines splits text on newlines, dropping carriage returns.
func splitLines(text string) []string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	return lines
}

// lineIndex maps byte offsets of a source to 1-based line numbers.
type lineIndex []int

// newLineIndex records the offset of every line start in src.
func newLineIndex(src []byte) lineIndex {
	idx := lineIndex{0}
	for i, b := range src {
		if b == '\n' {
			idx = append(idx, i+1)
		}
	}
	return idx
}

// line returns the 1-based line holding byte offset off.
func (idx lineIndex) line(off int64) int {
	return sort.Search(len(idx), func(i int) bool { return int64(idx[i]) > off })
}

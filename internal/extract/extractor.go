package extract

import "fmt"

// Extractor turns one file into records. Implementations are pure: they
// receive bytes and return records, and must not touch the store.
type Extractor interface {
	// Extract parses src. path is the file path the qualified names are
	// built from (for TS/JS, relative to the package root).
	Extract(path string, src []byte) (*FileResult, error)
}

// registry is written only from package init functions and read afterwards,
// so it needs no lock.
var registry = map[string]Extractor{}

// Register installs an extractor for a precise language name (see
// parse.LanguageForPath). Registering a language twice panics.
func Register(lang string, e Extractor) {
	if _, dup := registry[lang]; dup {
		panic(fmt.Sprintf("extract: language %q registered twice", lang))
	}
	registry[lang] = e
}

// For returns the extractor for a precise language name.
func For(lang string) (Extractor, bool) {
	e, ok := registry[lang]
	return e, ok
}

// APIExtractor is an Extractor with a mode for third-party packages
// (deps spec section 5), of which only the public API is indexed.
type APIExtractor interface {
	Extractor
	// ExtractAPI parses src like Extract and returns the declarations
	// visible outside the package only: exported top-level declarations
	// and their non-private members. Of the references it returns at most
	// the supertypes those declarations extend (TS/JS); imports are
	// returned where a package's files are found through them (TS/JS
	// re-exports).
	ExtractAPI(path string, src []byte) (*FileResult, error)
}

// APIFor returns the API extractor for a precise language name; false
// when the language has no extractor or no API mode.
func APIFor(lang string) (APIExtractor, bool) {
	e, ok := registry[lang].(APIExtractor)
	return e, ok
}

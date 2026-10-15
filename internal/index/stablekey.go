package index

import (
	"fmt"

	"github.com/cespare/xxhash/v2"

	"github.com/muralx/codedoc/internal/parse"
)

// pathHash is the hex xxhash of a file path; collision suffixes use its
// first four digits (spec 5.1), or eight when four are taken.
func pathHash(p string) string {
	return fmt.Sprintf("%016x", xxhash.Sum64String(p))
}

// hasSigHash reports whether the keys of symbols of a kind in a file of
// a precise language carry a signature hash: Java methods and
// constructors always do (spec 5.1).
func hasSigHash(lang, kind string) bool {
	return parse.Family(lang) == "java" && kind == "method"
}

package deps

import (
	"bytes"
	"os"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/parse"
)

// maxFileSize is the size above which a package file is skipped, as for
// workspace files (spec 7.1).
const maxFileSize = 2 << 20

// apiFile reads the file at the package-relative path rel below root and
// extracts its public API, charging it to b. ok is false when the file
// is not read: it is gone, resolves outside the package (see within),
// is too large, of no language with an API mode, or over the budget
// (see budget.take).
func apiFile(root, rel string, b *budget) (f File, res *extract.FileResult, ok bool) {
	lang := parse.LanguageForPath(rel)
	ex, hasAPI := extract.APIFor(lang)
	if !hasAPI {
		return File{}, nil, false
	}
	full, err := within(root, rel)
	if err != nil {
		return File{}, nil, false
	}
	info, err := os.Stat(full)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxFileSize || !b.take(info.Size()) {
		return File{}, nil, false
	}
	src, err := os.ReadFile(full)
	if err != nil {
		return File{}, nil, false
	}
	f = File{Path: rel, Lang: lang, Size: info.Size(), Mtime: info.ModTime().UnixNano(), Hash: index.ContentHash(src),
		Lines: countLines(src)}
	res, err = ex.ExtractAPI(rel, src)
	if err != nil {
		f.ParseErrors = 1
		return f, &extract.FileResult{}, true
	}
	f.ParseErrors, f.Symbols = res.ParseErrors, res.Symbols
	return f, res, true
}

// countLines counts the lines of a file, a last line without a line end
// included.
func countLines(data []byte) int {
	n := bytes.Count(data, []byte{'\n'})
	if len(data) > 0 && data[len(data)-1] != '\n' {
		n++
	}
	return n
}

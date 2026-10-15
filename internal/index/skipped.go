package index

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/muralx/codedoc/internal/exclude"
	"github.com/muralx/codedoc/internal/gitmeta"
	"github.com/muralx/codedoc/internal/parse"
)

// Skipped says why discovery leaves the file or directory at a
// repo-relative slash path out of the index, by the rules of Discover
// in their order: an excluded directory, a lock file or an exclude glob
// (exclude.Matcher), a path git ignores, a file above the size limit, a
// binary file, a language the repo does not select. The reason finishes
// the sentence "<path> is not indexed: ..."; it is "" for a path
// discovery keeps and for one that does not exist.
func Skipped(root, rel string, globs []string, langs map[string]bool) (string, error) {
	full := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Lstat(full)
	if err != nil {
		return "", nil
	}
	if why := exclude.New(root, globs).Why(rel, info.IsDir()); why != "" {
		return why, nil
	}
	if ignored, err := gitmeta.Ignored(root, rel); err != nil || ignored {
		return "git ignores it (.gitignore)", err
	}
	switch {
	case info.IsDir():
		return "", nil
	case !info.Mode().IsRegular():
		return "it is not a regular file", nil
	case Oversized(info.Size()) && parse.DocLanguageForPath(rel) == "":
		return fmt.Sprintf("it is larger than the limit of %d MB", SizeLimit()>>20), nil
	}
	sniff, err := readHead(full, binarySniffSize)
	if err != nil {
		return "", err
	}
	if bytes.IndexByte(sniff, 0) >= 0 {
		return "it is a binary file", nil
	}
	if lang := languageOf(rel, sniff); !allowed(langs, lang) {
		return "the languages of the repo in the config leave out " + lang, nil
	}
	return "", nil
}

// readHead reads the first n bytes of a file, fewer when it is shorter.
func readHead(path string, n int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, n)
	got, err := io.ReadFull(f, buf)
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		err = nil
	}
	return buf[:got], err
}

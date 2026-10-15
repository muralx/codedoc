package index_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/testutil"
)

// discoveredPaths returns the paths Discover lists under root.
func discoveredPaths(t *testing.T, root string, globs []string, langs map[string]bool) map[string]bool {
	t.Helper()
	files, _, err := index.Discover(root, globs, langs)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, f := range files {
		found[f.Path] = true
	}
	return found
}

// isRegular reports whether a path names a regular file.
func isRegular(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}

// skippedWhy maps paths of the fixture of the test below to the reason
// Skipped gives, "" for a path discovery keeps or that does not exist.
var skippedWhy = map[string]string{
	"debug.log":        "git ignores it (.gitignore)",
	"scratch/notes.go": "git ignores it (.gitignore)",
	"scratch":          "git ignores it (.gitignore)",
	"vendor/x/x.go":    "the directory vendor/ is excluded (dependencies, fixtures and build output are)",
	"dist":             "the directory dist/ is excluded (dependencies, fixtures and build output are)",
	"go.sum":           "dependency lock files are never indexed",
	"img.png":          "it is a binary file",
	"big.go":           "it is larger than the limit of 2 MB",
	"gen/api.go":       "it matches the exclude glob gen/**",
	"tool.py":          "the languages of the repo in the config leave out python",
	"link.mod":         "it is not a regular file",
	"big.md":           "", // a document over the limit is listed
	"store/store.go":   "",
	"store":            "",
	"empty.txt":        "",
	"no/such/file.go":  "",
}

// Skipped gives a reason for exactly the paths Discover leaves out.
func TestSkippedSaysWhyDiscoverLeavesAPathOut(t *testing.T) {
	root := testutil.CopyFixture(t, "go-lib")
	for rel, body := range map[string]string{
		".gitignore": "*.log\nscratch/\n", "debug.log": "x\n", "scratch/notes.go": "package scratch\n",
		"vendor/x/x.go": "package x\n", "dist/out.js": "x\n", "go.sum": "x\n", "img.png": "\x89PNG\x00\x00",
		"big.go": strings.Repeat("// pad\n", 400000), "big.md": strings.Repeat("pad pad\n", 400000),
		"gen/api.go": "package gen\n", "tool.py": "def tool():\n    pass\n", "empty.txt": "",
	} {
		writeFile(t, root, rel, []byte(body))
	}
	if err := os.Symlink(filepath.Join(root, "go.mod"), filepath.Join(root, "link.mod")); err != nil {
		t.Fatal(err)
	}
	globs := []string{"gen/**"}
	langs := map[string]bool{"go": true, "markdown": true, "text": true}
	want := skippedWhy
	found := discoveredPaths(t, root, globs, langs)
	for rel, why := range want {
		got, err := index.Skipped(root, rel, globs, langs)
		if err != nil || got != why {
			t.Errorf("%s: %q, %v; want %q", rel, got, err, why)
		}
		if isRegular(filepath.Join(root, rel)) && found[rel] != (why == "") {
			t.Errorf("%s: discovered %v, but the reason is %q", rel, found[rel], why)
		}
	}
	// Without a language selection every language is indexed.
	if got, err := index.Skipped(root, "tool.py", globs, nil); err != nil || got != "" {
		t.Errorf("tool.py without a selection: %q, %v", got, err)
	}
}

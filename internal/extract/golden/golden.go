// Package golden compares extractor output for the fixture workspace with
// JSON files stored next to this source file. Run the tests with -update to
// rewrite them.
package golden

import (
	"bytes"
	"encoding/json"
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/testutil"
)

var update = flag.Bool("update", false, "rewrite golden files")

// Source is one fixture source file.
type Source struct {
	Fixture string // fixture directory name, e.g. "go-lib"
	Path    string // slash path relative to the fixture root, e.g. "store/store.go"
	Abs     string // absolute path on disk
}

// Name returns the golden file name for the source: <fixture>/<path>.json.
func (s Source) Name() string { return s.Fixture + "/" + s.Path + ".json" }

// Sources lists, in path order, the files with one of exts under every
// fixture directory of testdata/ws whose name starts with prefix.
func Sources(t *testing.T, prefix string, exts ...string) []Source {
	t.Helper()
	ws := testutil.WorkspaceDir()
	var out []Source
	err := filepath.WalkDir(ws, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !slices.Contains(exts, filepath.Ext(p)) {
			return err
		}
		rel, err := filepath.Rel(ws, p)
		if err != nil {
			return err
		}
		fixture, path, _ := strings.Cut(filepath.ToSlash(rel), "/")
		if strings.HasPrefix(fixture, prefix) {
			out = append(out, Source{Fixture: fixture, Path: path, Abs: p})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatalf("no fixture sources for %s* %v", prefix, exts)
	}
	return out
}

// RunFixtures extracts every source and checks the result against its
// golden file. The extractor receives the fixture-relative path.
func RunFixtures(t *testing.T, e extract.Extractor, sources []Source) {
	t.Helper()
	for _, s := range sources {
		src, err := os.ReadFile(s.Abs)
		if err != nil {
			t.Fatal(err)
		}
		got, err := e.Extract(s.Path, src)
		if err != nil {
			t.Fatalf("%s: %v", s.Name(), err)
		}
		Check(t, s.Name(), got)
	}
}

// Check compares got, marshalled as indented JSON, with the golden file
// name, or rewrites the file when -update is set.
func Check(t *testing.T, name string, got any) {
	t.Helper()
	want, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want = append(want, '\n')
	path := filepath.Join(dir(), filepath.FromSlash(name))
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, want, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	have, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v (run with -update to create it)", name, err)
	}
	if !bytes.Equal(have, want) {
		t.Errorf("%s differs from golden file; got:\n%s", name, want)
	}
}

func dir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(file)
}

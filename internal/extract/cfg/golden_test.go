package cfg_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/muralx/codedoc/internal/extract/cfg"
	"github.com/muralx/codedoc/internal/extract/golden"
	"github.com/muralx/codedoc/internal/testutil"
)

// TestGoldenCfgRepo extracts every config file of the cfg-repo fixture and
// compares the result with golden/cfg-repo/<path>.json.
func TestGoldenCfgRepo(t *testing.T) {
	root := filepath.Join(testutil.WorkspaceDir(), "cfg-repo")
	seen := map[string]bool{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		format := cfg.FormatForPath(rel, src)
		if format == "" {
			return nil
		}
		seen[format] = true
		got, err := cfg.For(format).Extract(rel, src)
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		golden.Check(t, "cfg-repo/"+rel+".json", got)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 8 {
		t.Fatalf("fixture covers %d formats, want 8: %v", len(seen), seen)
	}
}

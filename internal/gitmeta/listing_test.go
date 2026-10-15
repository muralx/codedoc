package gitmeta_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/gitmeta"
	"github.com/muralx/codedoc/internal/testutil"
)

func TestIgnoredPaths(t *testing.T) {
	root := testutil.SourceRepo(t, "ig", map[string]string{".gitignore": "*.log\nout/\n", "a.go": "package a\n",
		"sub/b.go": "package sub\n"})
	for p, body := range map[string]string{"x.log": "x", "sub/y.log": "y", "out/bin/tool": "t", "out/z": "z", "new.go": "n"} {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := gitmeta.IgnoredPaths(root, ".")
	if err != nil || strings.Join(got, " ") != "out/ sub/y.log x.log" {
		t.Errorf("all = %v, %v", got, err)
	}
	if got, err := gitmeta.IgnoredPaths(root, "sub"); err != nil || strings.Join(got, " ") != "sub/y.log" {
		t.Errorf("sub = %v, %v", got, err)
	}
	if _, err := gitmeta.IgnoredPaths(t.TempDir(), "."); !errors.Is(err, gitmeta.ErrNotRepo) {
		t.Errorf("outside git: %v", err)
	}
}

func TestCommitTime(t *testing.T) {
	root := testutil.SourceRepo(t, "ct", map[string]string{"a.go": "package a\n"})
	at, err := gitmeta.CommitTime(root, "HEAD")
	if err != nil || at != testutil.FixtureTime.Unix() {
		t.Errorf("HEAD = %d, %v", at, err)
	}
	if _, err := gitmeta.CommitTime(root, "no-such-ref"); !errors.Is(err, gitmeta.ErrUnknownRef) {
		t.Errorf("unknown ref: %v", err)
	}
	if _, err := gitmeta.CommitTime(t.TempDir(), "HEAD"); !errors.Is(err, gitmeta.ErrNotRepo) {
		t.Errorf("outside git: %v", err)
	}
}

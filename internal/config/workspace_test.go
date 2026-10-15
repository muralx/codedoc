package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/muralx/codedoc/internal/config"
)

func TestWorkspaceForDirInsideConfigDirWithoutRepo(t *testing.T) {
	root := t.TempDir()
	body := "workspaces:\n  one:\n    repos: [{path: a}, {path: sub/b}]\n  two:\n    repos: [{path: sub/c}]\n"
	cfg, err := config.Load(writeConfig(t, root, body))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"a/pkg", "sub/b", "sub/c", "other"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cases := map[string]string{
		"a/pkg": "one", // inside a repo: the usual rule
		"sub/b": "one", // a repo root
		root:    "",    // repos of both workspaces below: ambiguous
		"sub":   "",    // likewise
		"other": "",    // no repo below
		"..":    "",    // outside the config directory
		"sub/c": "two", // a repo root of the other workspace
	}
	for dir, want := range cases {
		abs := dir
		if !filepath.IsAbs(dir) {
			abs = filepath.Join(root, dir)
		}
		checkWorkspaceFor(t, cfg, abs, want)
	}
	single, err := config.Load(writeConfig(t, root, "workspaces:\n  one:\n    repos: [{path: a}, {path: sub/b}]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if ws, err := single.WorkspaceFor(filepath.Join(root, "sub")); err != nil || ws.Name != "one" {
		t.Fatalf("%v %v", ws, err)
	}
}

// checkWorkspaceFor expects WorkspaceFor(dir) to pick want, or to fail
// when want is empty.
func checkWorkspaceFor(t *testing.T, cfg *config.Config, dir, want string) {
	t.Helper()
	ws, err := cfg.WorkspaceFor(dir)
	switch {
	case want == "" && err == nil:
		t.Errorf("%s: got %s, want an error", dir, ws.Name)
	case want != "" && (err != nil || ws.Name != want):
		t.Errorf("%s: got %v %v, want %s", dir, ws, err, want)
	}
}

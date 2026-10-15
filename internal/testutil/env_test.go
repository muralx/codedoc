package testutil_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/testutil"
)

func TestIsolateEnvMovesEveryDefaultDirectory(t *testing.T) {
	for _, name := range []string{"HOME", "XDG_STATE_HOME", "XDG_DATA_HOME", "XDG_CONFIG_HOME",
		"XDG_RUNTIME_DIR", "CODEDOC_DATA", "CODEDOC_CONFIG", "CODEDOC_GOMODCACHE", "XDG_CACHE_HOME", "GOPATH",
		"GRADLE_USER_HOME", "CARGO_HOME", "RUSTUP_HOME"} {
		t.Setenv(name, os.Getenv(name))
	}
	t.Setenv("GOPATH", "")
	realHome, _ := os.UserHomeDir()
	root, err := testutil.IsolateEnv()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	for _, name := range []string{"HOME", "XDG_STATE_HOME", "XDG_DATA_HOME", "XDG_CONFIG_HOME",
		"XDG_RUNTIME_DIR", "CODEDOC_DATA", "CODEDOC_GOMODCACHE"} {
		dir := os.Getenv(name)
		if !strings.HasPrefix(dir, root+string(filepath.Separator)) {
			t.Errorf("%s=%q, want under %s", name, dir, root)
		}
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			t.Errorf("%s: %v", name, err)
		}
	}
	if got := os.Getenv("CODEDOC_CONFIG"); got != filepath.Join(root, "CODEDOC_CONFIG", "config.yaml") {
		t.Errorf("CODEDOC_CONFIG=%q", got)
	}
	if got := os.Getenv("GOPATH"); got != filepath.Join(realHome, "go") {
		t.Errorf("GOPATH=%q, want the real home's", got)
	}
}

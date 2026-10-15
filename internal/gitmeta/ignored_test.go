package gitmeta_test

import (
	"testing"

	"github.com/muralx/codedoc/internal/gitmeta"
)

func TestIgnored(t *testing.T) {
	dir := initRepo(t)
	for path, want := range map[string]bool{"d.log": true, "sub/e.log": true, "a.go": false, "c.go": false, "nope.go": false} {
		if got, err := gitmeta.Ignored(dir, path); err != nil || got != want {
			t.Errorf("%s: %v, %v; want %v", path, got, err, want)
		}
	}
	// A directory that is no repository ignores nothing.
	if got, err := gitmeta.Ignored(t.TempDir(), "d.log"); err != nil || got {
		t.Errorf("outside a repo: %v, %v", got, err)
	}
	// Anything else git says is an error.
	if _, err := gitmeta.Ignored(dir, "../outside.log"); err == nil {
		t.Error("a path outside the repository is an error")
	}
}

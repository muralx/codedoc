package xdg_test

import (
	"path/filepath"
	"testing"

	"github.com/muralx/codedoc/internal/xdg"
)

func TestStateDir(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/s")
	if got, err := xdg.StateDir(); err != nil || got != "/s/codedoc" {
		t.Fatalf("%q %v", got, err)
	}
	t.Setenv("XDG_STATE_HOME", "relative")
	t.Setenv("HOME", "/h")
	if got, err := xdg.StateDir(); err != nil || got != filepath.Join("/h", ".local", "state", "codedoc") {
		t.Fatalf("%q %v", got, err)
	}
	t.Setenv("HOME", "")
	if _, err := xdg.StateDir(); err == nil {
		t.Fatal("no home: no error")
	}
}

// Package xdg locates codedoc's state directory under the XDG base
// directory layout.
package xdg

import (
	"os"
	"path/filepath"
)

// StateDir returns ${XDG_STATE_HOME:-~/.local/state}/codedoc, where the
// daemon log, the fallback socket, the write journal and the per-file
// locks live. A relative XDG_STATE_HOME is ignored, as the XDG spec
// requires.
func StateDir() (string, error) {
	state := os.Getenv("XDG_STATE_HOME")
	if !filepath.IsAbs(state) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		state = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(state, "codedoc"), nil
}

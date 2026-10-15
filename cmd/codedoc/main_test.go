package main

import (
	"bytes"
	"testing"

	"github.com/muralx/codedoc/internal/buildinfo"
)

// TestVersionCommand checks the version subcommand and the --version
// flag print the same line.
func TestVersionCommand(t *testing.T) {
	for _, arg := range []string{"version", "--version"} {
		var out bytes.Buffer
		cmd := newRootCmd()
		cmd.SetOut(&out)
		cmd.SetArgs([]string{arg})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("%s: %v", arg, err)
		}
		if got := out.String(); got != "codedoc "+buildinfo.Version+"\n" || buildinfo.Version == "" {
			t.Fatalf("%s: got %q", arg, got)
		}
	}
}

package main

import (
	"strings"
	"testing"
)

func TestAtDiffWithASpaceSuggestsEquals(t *testing.T) {
	e2eWorkspace(t)
	out, err := runCLI(t, "at", "MemStore.Get", "--ref", "HEAD~0", "--diff", "HEAD", "--workspace", "fx")
	if err == nil || !strings.Contains(err.Error()+out, "--diff=HEAD") {
		t.Fatalf("%v\n%s", err, out)
	}
}

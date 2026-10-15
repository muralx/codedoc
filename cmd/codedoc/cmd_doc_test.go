package main

import (
	"bytes"
	"strings"
	"testing"
)

// runCLIStdin runs codedoc with args and stdin.
func runCLIStdin(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	cmd := newRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestE2EDocSetAndCheck(t *testing.T) {
	e2eWorkspace(t)
	out, err := runCLIStdin(t, "Get returns the value under key.\n\nIt returns ErrMissing for an unknown key.\n",
		"doc", "set", "go-lib:store/store.go#MemStore.Get", "--workspace", "fx", "--verbose")
	if err != nil || !strings.Contains(out, "MemStore.Get  store/store.go:25-27  doc ok  Get returns the value under key.") {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err = runCLI(t, "doc", "set", "go-lib:service/", "--text", "Package service runs use cases.", "--workspace", "fx", "--verbose")
	if err != nil || !strings.Contains(out, "github.com/fixture/lib/service  service/service.go:1  doc ok") {
		t.Fatalf("%v\n%s", err, out)
	}
	if out, err = runCLIStdin(t, "", "doc", "set", "MemStore.Get", "--workspace", "fx"); err == nil {
		t.Fatalf("empty stdin accepted\n%s", out)
	}
	out, err = runCLI(t, "gaps", "--limit", "2", "--workspace", "fx")
	if err != nil || !strings.Contains(out, "== gaps (2)") || !strings.Contains(out, "[#1] [score ") {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err = runCLI(t, "doc", "check", "--workspace", "fx", "--repo", "go-lib")
	if err != nil || !strings.Contains(out, "== doc states (1)") || !strings.Contains(out, "go-lib  symbols ok:") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestE2EHistoryCommands(t *testing.T) {
	e2eWorkspace(t)
	for args, want := range map[string]string{
		"changes --since HEAD":                   "== changed files since HEAD (0)",
		"history MemStore.Get --limit 1":         "fixture: initial",
		"at MemStore.Get --ref HEAD":             "26\tfunc (m *MemStore) Get(key string) (string, error) {",
		"at MemStore.Get --ref HEAD --diff":      "no changes between HEAD and worktree",
		"at MemStore.Get --ref HEAD --diff=HEAD": "no changes between HEAD and HEAD",
		"overview":                               "== modules",
	} {
		out, err := runCLI(t, append(strings.Fields(args), "--workspace", "fx", "--repo", "go-lib")...)
		if err != nil || !strings.Contains(out, want) {
			t.Errorf("%s: %v\n%s", args, err, out)
		}
	}
}

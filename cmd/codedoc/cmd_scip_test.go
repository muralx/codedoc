package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// stubIndexer writes an executable shell script running body and returns
// its path. Indexers are called as `<bin> --output <file>` (Go) or
// `<bin> index --output <file>` (TypeScript).
func stubIndexer(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "indexer")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// wantLines fails unless every pattern matches out in multi-line mode.
func wantLines(t *testing.T, out string, patterns ...string) {
	t.Helper()
	for _, p := range patterns {
		if !regexp.MustCompile(`(?m)` + p).MatchString(out) {
			t.Fatalf("%s not in\n%s", p, out)
		}
	}
}

// cli runs the CLI and fails on an error.
func cli(t *testing.T, args ...string) string {
	t.Helper()
	out, err := runCLI(t, args...)
	if err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out)
	}
	return out
}

func TestSCIPRunMergesPreciseEdges(t *testing.T) {
	setupWorkspace(t)
	fixture, err := filepath.Abs("../../internal/scip/testdata/go-lib.scip")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEDOC_SCIP_GO", stubIndexer(t, `cp '`+fixture+`' "$2"`))
	t.Setenv("CODEDOC_SCIP_TS", filepath.Join(t.TempDir(), "no-scip-typescript"))
	cli(t, "index", "--workspace", "fx")
	wantLines(t, cli(t, "callees", "Service.Lookup", "--workspace", "fx", "--keys", "--detail"), `^\s*\S+:Store\.Get .*\[import\]$`)
	out := cli(t, "scip", "run", "--workspace", "fx")
	wantLines(t, out, `^go-lib  go: ran precise:\+\d+ \(refs added:\d+\) implements:2 mapped:\d+ files:5 changed-since:0 dropped:0$`,
		`^ts-domain  ts: missing indexer .*no-scip-typescript$`)
	if n := strings.Count(out, "warning:"); n != 1 {
		t.Fatalf("%d missing indexer warnings:\n%s", n, out)
	}
	after := cli(t, "callees", "Service.Lookup", "--workspace", "fx", "--detail")
	if strings.Contains(after, "[import]") || strings.Count(after, "[precise]") != 2 {
		t.Fatalf("after:\n%s", after)
	}
	wantLines(t, cli(t, "status", "--workspace", "fx"), `^go-lib .* scip:[0-9a-f]{7}$`,
		`^ts-domain .* scip:missing no-scip-typescript$`)
}

func TestSCIPRunReportsFailedIndexers(t *testing.T) {
	setupWorkspace(t)
	t.Setenv("CODEDOC_SCIP_GO", stubIndexer(t, "echo boom >&2; exit 1"))
	t.Setenv("CODEDOC_SCIP_TS", stubIndexer(t, `echo garbage > "$3"`))
	out, err := runCLI(t, "scip", "run", "--workspace", "fx", "--repo", "go-lib")
	if err == nil || err.Error() != "1 SCIP indexer run(s) failed" || !strings.Contains(out, "go-lib  go: failed: scip: go indexer") ||
		!strings.Contains(out, "boom") {
		t.Fatalf("%v\n%s", err, out)
	}
	wantLines(t, cli(t, "status", "--workspace", "fx"), `^go-lib .* scip:failed\(\d{4}-\d\d-\d\d\)$`)
	fixture, err := filepath.Abs("../../internal/scip/testdata/go-lib.scip")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEDOC_SCIP_GO", stubIndexer(t, `cp '`+fixture+`' "$2"`))
	cli(t, "scip", "run", "--workspace", "fx", "--repo", "go-lib")
	wantLines(t, cli(t, "status", "--workspace", "fx"), `^go-lib .* scip:[0-9a-f]{7}$`)
	out, err = runCLI(t, "scip", "run", "--workspace", "fx", "--repo", "ts-domain")
	if err == nil || !strings.Contains(out, "ts-domain  ts: failed: scip: parse") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestSCIPRef(t *testing.T) {
	if scipRef("") != "scip:never" || scipRef("0123456789") != "scip:0123456" || scipRef("ab") != "scip:ab" {
		t.Fatal("scip refs")
	}
}

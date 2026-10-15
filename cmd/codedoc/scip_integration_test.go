//go:build integration

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/testutil"
)

// TestIntegrationSCIPRun runs `codedoc scip run` with the real scip-go
// and scip-typescript on copies of the go-lib and ts-domain fixtures. It
// fails when an indexer is missing: the Docker image must provide them.
func TestIntegrationSCIPRun(t *testing.T) {
	for _, bin := range []string{"scip-go", "scip-typescript"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Fatalf("%s not on PATH: %v", bin, err)
		}
	}
	goLib, tsDomain := testutil.CopyFixture(t, "go-lib"), testutil.CopyFixture(t, "ts-domain")
	cfgDir := t.TempDir()
	body := "workspaces:\n  fx:\n    repos: [{path: " + goLib + "}, {path: " + tsDomain + "}]\n"
	cfgPath := filepath.Join(cfgDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEDOC_CONFIG", cfgPath)
	t.Setenv("CODEDOC_DATA", filepath.Join(cfgDir, "data"))
	out := cli(t, "scip", "run", "--workspace", "fx")
	t.Log(out)
	wantLines(t, out, `^go-lib  go: ran precise:\+[1-9]`, `^ts-domain  ts: ran precise:\+[1-9]`)
	// The tier of a certain edge is a tag of --detail: the default
	// prints only the tags that change what the reader does.
	wantLines(t, cli(t, "callees", "Service.Lookup", "--workspace", "fx", "--detail"), `Store\.Get .*\[precise\]$`)
	if plain := cli(t, "callees", "Service.Lookup", "--workspace", "fx"); strings.Contains(plain, "[precise]") ||
		!strings.Contains(plain, "  Store.Get  :18  v, err := svc.s.Get(key)\n") {
		t.Fatalf("callees without --detail:\n%s", plain)
	}
	if impls := cli(t, "impls", "go-lib:store/store.go#Store", "--workspace", "fx", "--detail"); strings.Count(impls, "[precise]") != 2 {
		t.Fatalf("impls:\n%s", impls)
	}
	wantLines(t, cli(t, "supertypes", "MemoryRepo", "--workspace", "fx", "--detail"), `EnvironmentRepo .*\[precise\]$`)
	if status := cli(t, "status", "--workspace", "fx"); strings.Contains(status, "scip:never") {
		t.Fatalf("status:\n%s", status)
	}
}

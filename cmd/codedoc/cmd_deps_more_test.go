package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The listing indexes what a query-sized budget allows and reports
// failures with their error; codedoc deps index retries them.
func TestDepsIndexRetriesFailedPackages(t *testing.T) {
	repos := depsWorkspace(t, "")
	manifest := filepath.Join(repos["deps-app"], "node_modules", "dup", "package.json")
	good, _ := os.ReadFile(manifest)
	cli(t, "index", "--workspace", "fx")
	cli(t, "deps", "prune", "--all")
	if err := os.WriteFile(manifest, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := cli(t, "deps", "--workspace", "fx", "--repo", "deps-app")
	if !strings.Contains(out, "npm/dup@2.0.0  package.json  failed declared:^2.0.0 error: ") || !strings.Contains(out, "1 failed") {
		t.Fatalf("a failed package is listed with its error:\n%s", out)
	}
	if err := os.WriteFile(manifest, good, 0o644); err != nil {
		t.Fatal(err)
	}
	out = cli(t, "deps", "index", "--workspace", "fx", "--repo", "deps-app", "--verbose")
	hasLines(t, out, "npm/dup@2.0.0  package.json  indexed declared:^2.0.0 symbols:2 vectors:0/2",
		"indexed 1 package version(s). "+unusedLine("3"))
}

func TestDepsPrune(t *testing.T) {
	repos := depsWorkspace(t, "")
	if out := cli(t, "deps", "prune", "--verbose"); !strings.Contains(out, "== pruned (0)") {
		t.Fatalf("without a deps.db nothing is pruned:\n%s", out)
	}
	cli(t, "index", "--workspace", "fx")
	if out := cli(t, "deps", "prune", "--verbose"); !strings.Contains(out, "== pruned (0)") {
		t.Fatalf("every version is in use:\n%s", out)
	}
	// Dropping a dependency leaves its version unused.
	manifest := filepath.Join(repos["deps-go"], "go.mod")
	raw, _ := os.ReadFile(manifest)
	if err := os.WriteFile(manifest, []byte(strings.Replace(string(raw), "\tgithub.com/acme/kit v1.4.0\n", "", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	cli(t, "index", "--workspace", "fx")
	out := cli(t, "deps", "prune", "--verbose")
	hasLines(t, out, "== pruned (1)", "go/github.com/acme/kit@v1.4.0")
	if !strings.HasPrefix(out, "== pruned (1)\n") {
		t.Fatalf("the shared database has no repo to head the answer:\n%s", out)
	}
	out = cli(t, "deps", "prune", "--all", "--format", "json")
	if strings.Count(out, `"key"`) != 5 {
		t.Fatalf("--all deletes the five versions left:\n%s", out)
	}
	out = cli(t, "deps", "index", "--workspace", "fx", "--verbose")
	hasLines(t, out, "indexed 5 package version(s). "+unusedLine("4"))
	if _, err := runCLI(t, "deps", "prune", "--format", "xml"); err == nil {
		t.Error("an unknown format must fail")
	}
}

func TestDepsConfigIncludeExcludeAndDisabled(t *testing.T) {
	depsWorkspace(t, "    deps:\n      include: [transitive-dep, nowhere-pkg]\n      exclude: [\"@acme/*\", dup]\n")
	cli(t, "index", "--workspace", "fx")
	out := cli(t, "deps", "--workspace", "fx")
	hasLines(t, out, "npm/transitive-dep@5.1.0  deps-app:package.json  indexed symbols:1 vectors:0/1",
		"deps.include: nowhere-pkg is not installed. "+unusedLine("3"))
	if strings.Contains(out, "@acme/sdk") || strings.Contains(out, "npm/dup") {
		t.Errorf("excluded packages are listed:\n%s", out)
	}
	depsWorkspace(t, "    deps:\n      enabled: false\n")
	out = cli(t, "index", "--workspace", "fx")
	if strings.Contains(out, "== deps") {
		t.Errorf("deps disabled, yet indexed:\n%s", out)
	}
	if out := cli(t, "deps", "--workspace", "fx"); !strings.Contains(out, "== deps (0)") {
		t.Errorf("deps disabled, yet listed:\n%s", out)
	}
}

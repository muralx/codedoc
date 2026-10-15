package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/deps"
	"github.com/muralx/codedoc/internal/testutil"
)

// depsWorkspace writes a config with workspace "fx" holding copies of
// the dependency fixtures, selects the fixture module cache and returns
// the repo copies by name.
func depsWorkspace(t *testing.T, extra string) map[string]string {
	t.Helper()
	repos := map[string]string{}
	var list []string
	for _, name := range []string{"deps-app", "deps-go"} {
		repos[name] = testutil.CopyFixture(t, name)
		list = append(list, "{path: "+repos[name]+"}")
	}
	dir := t.TempDir()
	body := "workspaces:\n  fx:\n    repos: [" + strings.Join(list, ", ") + "]\n" + extra
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEDOC_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("CODEDOC_DATA", filepath.Join(dir, "data"))
	t.Setenv(deps.ModCacheEnv, testutil.ModCacheDir())
	return repos
}

// unusedLine is the note of a deps listing with n unused packages.
func unusedLine(n string) string {
	return n + " unused: declared but imported by no source file, so not indexed; name a package in deps.include to " +
		"index it anyway"
}

// hasLines fails the test unless out holds every line.
func hasLines(t *testing.T, out string, lines ...string) {
	t.Helper()
	for _, l := range lines {
		if !strings.Contains("\n"+out, "\n"+l+"\n") {
			t.Errorf("missing line %q in:\n%s", l, out)
		}
	}
}

func TestIndexDiscoversAndIndexesDependencies(t *testing.T) {
	depsWorkspace(t, "")
	out := cli(t, "index", "--workspace", "fx", "--verbose")
	hasLines(t, out, "== deps (2)",
		"deps-app  indexed:5 pending:0 missing:1 failed:0 skipped:1 unused:3",
		"deps-go  indexed:1 pending:0 missing:2 failed:0 skipped:0 unused:1",
		"indexed 6 package version(s)")
	if _, err := os.Stat(filepath.Join(os.Getenv("CODEDOC_DATA"), "deps.db")); err != nil {
		t.Fatalf("deps.db: %v", err)
	}
	// --rebuild recreates the workspace database and keeps deps.db.
	out = cli(t, "index", "--workspace", "fx", "--rebuild", "--verbose")
	hasLines(t, out, "deps-app  indexed:5 pending:0 missing:1 failed:0 skipped:1 unused:3")
	if strings.Contains(out, "package version(s)") {
		t.Errorf("a rebuild must find the packages in deps.db:\n%s", out)
	}
}

func TestDepsCommandListsUsesWithStates(t *testing.T) {
	depsWorkspace(t, "")
	cli(t, "index", "--workspace", "fx")
	out := cli(t, "deps", "--workspace", "fx")
	hasLines(t, out, "== deps (14)",
		"npm/@acme/sdk@1.2.0  deps-app:package.json  indexed declared:^1.2.0 symbols:18 vectors:0/14",
		"npm/@types/node@22.1.0  deps-app:package.json  indexed declared:^22.0.0 symbols:4 vectors:0/5 modules:2",
		"npm/lint-tool@9.4.0  deps-app:package.json  unused declared:^9.0.0",
		"npm/plain-js@2.0.1  deps-app:package.json  unused declared:^2.0.0",
		"npm/@acme/sdk@1.2.0  deps-app:packages/web/package.json  unused declared:^1.2.0",
		"go/github.com/BurntSushi/toml@v0.3.1  deps-go:go.mod  unused declared:v0.3.1", unusedLine("4"),
		"npm/@fixture/web  deps-app:package.json  skipped declared:workspace:*",
		"npm/left-pad  deps-app:package.json  missing declared:^1.3.0",
		"npm/dup@1.0.0  deps-app:packages/web/package.json  indexed declared:^1.0.0 symbols:1 vectors:0/1",
		"go/github.com/acme/kit@v1.4.0  deps-go:go.mod  indexed declared:v1.4.0 symbols:16 vectors:0/17",
		"go/github.com/acme/absent  deps-go:go.mod  missing declared:v0.1.0")
	out = cli(t, "deps", "--workspace", "fx", "--repo", "deps-go")
	hasLines(t, out, "== deps (4)", "go/github.com/acme/kit@v1.4.0  go.mod  indexed declared:v1.4.0 symbols:16 vectors:0/17",
		unusedLine("1"))
	if !strings.HasPrefix(out, "deps-go@") {
		t.Errorf("one repo gets a repo header:\n%s", out)
	}
	if _, err := runCLI(t, "deps", "--workspace", "fx", "--format", "xml"); err == nil {
		t.Error("an unknown format must fail")
	}
}

func TestDepsCommandJSON(t *testing.T) {
	depsWorkspace(t, "")
	cli(t, "index", "--workspace", "fx")
	out := cli(t, "deps", "--workspace", "fx", "--repo", "deps-app", "--format", "json")
	if !json.Valid([]byte(out)) {
		t.Fatalf("invalid JSON:\n%s", out)
	}
	for _, want := range []string{`"state":"indexed"`, `"name":"@acme/sdk"`, `"version":"1.2.0"`, `"declared":"^1.2.0"`,
		`"symbols":18`, `"manifest":"packages/web/package.json"`, `"state":"missing"`, `"eco":"npm"`, `"state":"unused"`,
		`"name":"lint-tool"`, `"modules":["fs","path"]`, `"note":"3unused:declaredbutimportedbynosourcefile`} {
		if !strings.Contains(strings.ReplaceAll(out, " ", ""), want) {
			t.Errorf("missing %s in:\n%s", want, out)
		}
	}
}

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/deps"
	"github.com/muralx/codedoc/internal/testutil"
)

// writeTree writes files (relative path: content) below root.
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for p, content := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// sourceWorkspaces writes a config with one workspace per entry of
// repos, each holding one repo made of the given files, points the data
// directory and the Go module cache at fresh directories and returns
// the repo paths by workspace and the module cache.
func sourceWorkspaces(t *testing.T, repos map[string]map[string]string) (paths map[string]string, modCache string) {
	t.Helper()
	paths = map[string]string{}
	body := "workspaces:\n"
	for ws, files := range repos {
		paths[ws] = testutil.SourceRepo(t, "app", files)
		body += "  " + ws + ":\n    repos: [{path: " + paths[ws] + "}]\n"
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	modCache = filepath.Join(dir, "gomodcache")
	t.Setenv("CODEDOC_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("CODEDOC_DATA", filepath.Join(dir, "data"))
	t.Setenv(deps.ModCacheEnv, modCache)
	return paths, modCache
}

func TestGoPackageIsAddressedByItsDeclaredName(t *testing.T) {
	_, cache := sourceWorkspaces(t, map[string]map[string]string{"w": {
		"go.mod":  "module example.com/app\n\ngo 1.22\n\nrequire gopkg.in/yaml.v3 v3.0.1\n",
		"main.go": "package main\n\nimport \"gopkg.in/yaml.v3\"\n\nfunc main() { _ = yaml.Unmarshal(nil, nil) }\n",
	}})
	writeTree(t, filepath.Join(cache, "gopkg.in/yaml.v3@v3.0.1"), map[string]string{
		"go.mod":  "module gopkg.in/yaml.v3\n",
		"yaml.go": "// Package yaml reads YAML.\npackage yaml\n\n// Unmarshal decodes.\nfunc Unmarshal(in []byte, out any) error { return nil }\n",
	})
	cli(t, "index", "--workspace", "w")
	for _, addr := range []string{"yaml.Unmarshal", "v3.Unmarshal"} {
		out := cli(t, "symbol", addr, "--deps", "--workspace", "w", "--keys")
		if !strings.Contains(out, "go/gopkg.in/yaml.v3@v3.0.1:go:gopkg.in/yaml.v3:Unmarshal") {
			t.Errorf("symbol %s --deps:\n%s", addr, out)
		}
	}
}

// goApp is a repo requiring the fixture module kit.
func goApp(module string) map[string]string {
	return map[string]string{
		"go.mod":  "module " + module + "\n\ngo 1.22\n\nrequire example.com/kit v1.0.0\n",
		"main.go": "package main\n\nimport \"example.com/kit\"\n\nfunc main() { kit.Run() }\n",
	}
}

// A prune run under a config that lists fewer workspaces must not take
// the packages of the others; and when their packages do vanish (prune
// --all, a deleted deps.db) the next query brings them back instead of
// answering empty.
func TestPruneUnderAnotherConfigKeepsOtherWorkspacesPackages(t *testing.T) {
	_, cache := sourceWorkspaces(t, map[string]map[string]string{"one": goApp("example.com/one"), "two": goApp("example.com/two")})
	writeTree(t, filepath.Join(cache, "example.com/kit@v1.0.0"), map[string]string{
		"go.mod": "module example.com/kit\n",
		"kit.go": "package kit\n\n// Run runs.\nfunc Run() {}\n",
	})
	cli(t, "index", "--workspace", "one")
	cli(t, "index", "--workspace", "two")
	full := os.Getenv("CODEDOC_CONFIG")
	raw, err := os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	narrow := filepath.Join(t.TempDir(), "narrow.yaml")
	body := string(raw)
	if err := os.WriteFile(narrow, []byte(body[:strings.Index(body, "  two:")]+"  empty:\n    repos: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEDOC_CONFIG", narrow)
	out := cli(t, "deps", "prune", "--verbose")
	if !strings.Contains(out, "== pruned (0)") || !strings.Contains(out, "kept the packages of 2 workspace database(s)") {
		t.Fatalf("prune under the narrow config:\n%s", out)
	}
	out = cli(t, "deps", "prune", "--all", "--dry-run")
	hasLines(t, out, "== would prune (1)", "go/example.com/kit@v1.0.0")
	hasLines(t, cli(t, "deps", "prune", "--all", "--verbose"), "== pruned (1)", "go/example.com/kit@v1.0.0")
	t.Setenv("CODEDOC_CONFIG", full)
	for _, step := range []func(){func() {}, func() {
		if err := os.Remove(filepath.Join(os.Getenv("CODEDOC_DATA"), "deps.db")); err != nil {
			t.Fatal(err)
		}
	}} {
		step()
		out = cli(t, "find", "Run", "--deps", "--workspace", "two", "--keys")
		if !strings.Contains(out, "go/example.com/kit@v1.0.0:go:example.com/kit:Run") {
			t.Fatalf("a query after the packages vanished:\n%s", out)
		}
	}
}

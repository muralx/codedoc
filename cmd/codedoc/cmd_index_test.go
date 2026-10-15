package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/testutil"
)

// setupWorkspace writes a config with a workspace "fx" holding copies of
// go-lib and ts-domain, and points codedoc at it. It returns the config
// directory and the go-lib copy.
func setupWorkspace(t *testing.T) (cfgDir, goLib string) {
	t.Helper()
	goLib = testutil.CopyFixture(t, "go-lib")
	tsDomain := testutil.CopyFixture(t, "ts-domain")
	cfgDir = t.TempDir()
	body := "workspaces:\n  fx:\n    repos: [{path: " + goLib + "}, {path: " + tsDomain + "}]\n    exclude: [\"**/*.test.ts\"]\n"
	cfgPath := filepath.Join(cfgDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEDOC_CONFIG", cfgPath)
	t.Setenv("CODEDOC_DATA", filepath.Join(cfgDir, "data"))
	return cfgDir, goLib
}

func runCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	cmd := newRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestIndexCommandIndexesWorkspaceIncrementally(t *testing.T) {
	setupWorkspace(t)
	out, err := runCLI(t, "index", "--workspace", "fx", "--verbose")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^go-lib@\w+ ts-domain@\w+\n== index \(2\)\n`).MatchString(out) {
		t.Fatalf("got %q", out)
	}
	if !regexp.MustCompile(`(?m)^go-lib  main@[0-9a-f]{7} files:\+8 ~0 -0$`).MatchString(out) {
		t.Fatalf("go-lib line missing:\n%s", out)
	}
	if !regexp.MustCompile(`(?m)^ts-domain  main@[0-9a-f]{7} files:\+4 ~0 -0$`).MatchString(out) {
		t.Fatalf("ts-domain line missing (test file excluded):\n%s", out)
	}
	out, err = runCLI(t, "index", "--workspace", "fx", "--repo", "go-lib", "--verbose")
	if err != nil || !regexp.MustCompile(`(?m)^go-lib  main@[0-9a-f]{7} files:\+0 ~0 -0$`).MatchString(out) {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.HasPrefix(out, "go-lib@") {
		t.Fatalf("single repo header should carry its name and head: %q", out)
	}
}

func TestIndexHeadersCountDirtyFiles(t *testing.T) {
	_, goLib := setupWorkspace(t)
	if err := os.WriteFile(filepath.Join(goLib, "new.go"), []byte("package lib\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runCLI(t, "index", "--workspace", "fx", "--verbose")
	if err != nil || !regexp.MustCompile(`^go-lib@\w+ ts-domain@\w+ dirty\n`).MatchString(out) {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err = runCLI(t, "index", "--workspace", "fx", "--repo", "go-lib", "--format", "json")
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Header struct {
			Repo      string
			Dirty     int
			Workspace string
		}
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil || resp.Header.Repo != "go-lib" ||
		resp.Header.Dirty != 1 || resp.Header.Workspace != "" {
		t.Fatalf("%v %+v\n%s", err, resp.Header, out)
	}
}

func TestIndexCommandResolvesWorkspaceFromCwd(t *testing.T) {
	_, goLib := setupWorkspace(t)
	t.Chdir(filepath.Join(goLib, "store"))
	out, err := runCLI(t, "index", "--format", "json")
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Sections []struct{ Entries []struct{ Key string } }
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil || len(resp.Sections[0].Entries) != 2 {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestIndexCommandErrors(t *testing.T) {
	setupWorkspace(t)
	if _, err := runCLI(t, "index", "--workspace", "nope"); err == nil || !strings.Contains(err.Error(), `"nope" (configured: fx)`) {
		t.Fatalf("unknown workspace: %v", err)
	}
	if _, err := runCLI(t, "index", "--workspace", "fx", "--repo", "nope"); err == nil {
		t.Fatal("expected unknown repo error")
	}
	t.Chdir(t.TempDir())
	if _, err := runCLI(t, "status"); err == nil ||
		!strings.Contains(err.Error(), "not inside any configured repository; use --workspace NAME (configured: fx)") {
		t.Fatalf("outside any workspace: %v", err)
	}
	t.Setenv("CODEDOC_CONFIG", filepath.Join(t.TempDir(), "missing.yaml"))
	if _, err := runCLI(t, "index", "--workspace", "fx"); err == nil {
		t.Fatal("expected config error")
	}
}

func TestIndexRebuildRecreatesDatabase(t *testing.T) {
	cfgDir, _ := setupWorkspace(t)
	if _, err := runCLI(t, "index", "--workspace", "fx"); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(cfgDir, "data", "fx.db")
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(db + suffix)
	}
	if err := os.WriteFile(db, []byte("not a database, just garbage bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runCLI(t, "index", "--workspace", "fx"); err == nil {
		t.Fatal("expected error on corrupt database")
	}
	out, err := runCLI(t, "index", "--workspace", "fx", "--rebuild", "--verbose")
	if err != nil || !strings.Contains(out, "files:+8 ~0 -0") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestStatusCommand(t *testing.T) {
	_, goLib := setupWorkspace(t)
	out, err := runCLI(t, "status", "--workspace", "fx")
	if err != nil || !strings.Contains(out, "go-lib  not indexed") || !strings.Contains(out, "fresh:no") {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := runCLI(t, "index", "--workspace", "fx", "--repo", "go-lib"); err != nil {
		t.Fatal(err)
	}
	out, err = runCLI(t, "status", "--workspace", "fx", "--repo", "go-lib")
	if err != nil || !regexp.MustCompile(`^go-lib@[0-9a-f]{7}\n== status`).MatchString(out) {
		t.Fatalf("clean tree at the indexed commit should be fresh: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(goLib, "new.go"), []byte("package lib\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = runCLI(t, "status", "--workspace", "fx", "--repo", "go-lib")
	if err != nil {
		t.Fatal(err)
	}
	re := `(?m)^go-lib@[0-9a-f]{7} dirty fresh:no\n== status \(1\)\n` +
		`go-lib  main@[0-9a-f]{7} files:8 symbols:24 config-keys:0 unresolved-config:1 parse-errors:0 vectors:0/18 dirty:1 ` +
		`indexed:\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ scip:never ` +
		`\(index behind: 1 file changed since indexed; the next query or codedoc index brings it up to date\)$`
	if !regexp.MustCompile(re).MatchString(out) {
		t.Fatalf("got\n%s", out)
	}
}

func TestUnknownFormatIsRejectedBeforeIndexing(t *testing.T) {
	cfgDir, _ := setupWorkspace(t)
	for _, cmd := range []string{"index", "status"} {
		_, err := runCLI(t, cmd, "--workspace", "fx", "--format", "yaml")
		if err == nil || !strings.Contains(err.Error(), "yaml") {
			t.Fatalf("%s: %v", cmd, err)
		}
	}
	if _, err := os.Stat(filepath.Join(cfgDir, "data", "fx.db")); !os.IsNotExist(err) {
		t.Fatalf("database created despite bad format: %v", err)
	}
}

func TestIndexWarnsAboutDuplicateIdentities(t *testing.T) {
	lib1, lib2 := testutil.CopyFixture(t, "go-lib"), testutil.CopyFixture(t, "go-lib")
	app := testutil.CopyFixture(t, "go-app")
	cfgDir := t.TempDir()
	body := "workspaces:\n  fx:\n    repos: [{path: " + lib2 + ", name: lib2}, {path: " + app + "}, {path: " + lib1 + ", name: lib1}]\n"
	cfgPath := filepath.Join(cfgDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEDOC_CONFIG", cfgPath)
	t.Setenv("CODEDOC_DATA", filepath.Join(cfgDir, "data"))
	var out, errOut bytes.Buffer
	cmd := newRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"index", "--workspace", "fx"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	want := "warning: go identity github.com/fixture/lib is provided by repos lib2, lib1; " +
		"imports from other repos resolve to lib2\n"
	if errOut.String() != want {
		t.Fatalf("stderr %q, want %q", errOut.String(), want)
	}
	if strings.Contains(out.String(), "warning") {
		t.Fatalf("warning on stdout: %s", out.String())
	}
}

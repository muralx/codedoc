package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/testutil"
)

func indexed(t *testing.T) (cfgDir, goLib string) {
	t.Helper()
	cfgDir, goLib = setupWorkspace(t)
	if _, err := runCLI(t, "index", "--workspace", "fx"); err != nil {
		t.Fatal(err)
	}
	return cfgDir, goLib
}

func TestOutlineCommandFromCwd(t *testing.T) {
	_, goLib := indexed(t)
	t.Chdir(goLib)
	out, err := runCLI(t, "outline", "store/store.go")
	if err != nil || !strings.HasPrefix(out, "go-lib@") || strings.Contains(out, "fresh") ||
		!strings.Contains(out, "\nMemStore.Get  :26-32  func (m *MemStore) Get(key string) (string, error)  Get implements Store.\n") {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err = runCLI(t, "outline", "store/store.go", "--keys")
	if err != nil || !strings.Contains(out, "\ngo-lib:go:github.com/fixture/lib/store:MemStore.Get  :26-32  ") {
		t.Fatalf("--keys: %v\n%s", err, out)
	}
}

func TestFindCommandFiltersAndBudget(t *testing.T) {
	indexed(t)
	out, err := runCLI(t, "find", "Get", "--workspace", "fx", "--kind", "method", "--repo", "go-lib")
	if err != nil || strings.Count(out, ".Get  ") != 4 || strings.Contains(out, "go-lib:go:") {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err = runCLI(t, "find", "Get", "--workspace", "fx", "--kind", "method", "--repo", "go-lib", "--keys")
	if err != nil || strings.Count(out, "go-lib:go:github.com/fixture/lib/store:") != 3 {
		t.Fatalf("--keys: %v\n%s", err, out)
	}
	// What a budget leaves out is one pasted command away.
	out, err = runCLI(t, "find", "*", "--workspace", "fx", "--budget", "300")
	if err != nil || len(out) > 300 || !strings.Contains(out, " more: codedoc find '*' --budget 300 --workspace fx --offset ") {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err = runCLI(t, "find", "*", "--workspace", "fx", "--offset", "1000", "--format", "json")
	if err != nil || !strings.HasPrefix(out, `{"header":`) {
		t.Fatalf("%v\n%s", err, out)
	}
	if out, err := runCLI(t, "find", "Nothing", "--workspace", "fx"); err != nil {
		t.Fatalf("find with no match must exit 0: %v\n%s", err, out)
	}
}

func TestSymbolCommandExitCodes(t *testing.T) {
	indexed(t)
	out, err := runCLI(t, "symbol", "Get", "--workspace", "fx", "--strict")
	if !errors.Is(err, ErrAmbiguous) || strings.Count(out, ".Get  ") != 4 {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, addr := range []string{"Nope", "go-lib:go:store:", "go-lib:go:store:#abcd"} {
		if _, err := runCLI(t, "symbol", addr, "--workspace", "fx", "--strict"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%s: %v", addr, err)
		}
	}
	// The source is the default; --body is still accepted.
	for _, args := range [][]string{{"symbol", "MemStore.Get", "--workspace", "fx", "--body"}, {"symbol", "MemStore.Get", "--workspace", "fx"}} {
		out, err = runCLI(t, args...)
		if err != nil || !strings.HasSuffix(out, "\nMemStore.Get  store/store.go:26-32\n25\t// Get implements Store.\n"+
			"26\tfunc (m *MemStore) Get(key string) (string, error) {\n27\t\tv, ok := m.data[key]\n28\t\tif !ok {\n"+
			"29\t\t\treturn \"\", ErrMissing\n30\t\t}\n31\t\treturn v, nil\n32\t}\n") {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	out, err = runCLI(t, "symbol", "MemStore.Get", "--workspace", "fx", "--no-body")
	if err != nil || strings.Contains(out, "26\t") || !strings.Contains(out, "func (m *MemStore) Get(key string) (string, error)  Get implements Store.\n") {
		t.Fatalf("--no-body: %v\n%s", err, out)
	}
	if _, err := runCLI(t, "symbol", "MemStore.Get", "--workspace", "fx", "--no-body", "--context", "2"); err == nil {
		t.Fatal("--no-body with --context accepted")
	}
}

func TestSymbolCommandDocOnlyAndContext(t *testing.T) {
	indexed(t)
	out, err := runCLI(t, "symbol", "MemStore.Get", "--workspace", "fx", "--context", "1")
	if err != nil || !strings.Contains(out, "25\t// Get implements Store.") || !strings.Contains(out, "33\t\n") {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err = runCLI(t, "symbol", "MemStore.Get", "--workspace", "fx", "--doc-only")
	if err != nil || !strings.Contains(out, "Get implements Store.") || strings.Contains(out, "26\t") {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := runCLI(t, "symbol", "MemStore.Get", "--workspace", "fx", "--doc-only", "--body"); err == nil {
		t.Fatal("--doc-only with --body accepted")
	}
	if _, err := runCLI(t, "symbol", "MemStore.Get", "--workspace", "fx", "--context", "-1"); err == nil {
		t.Fatal("negative --context accepted")
	}
}

func TestQueryOutWritesFullResultToFile(t *testing.T) {
	cfgDir, _ := indexed(t)
	dst := filepath.Join(cfgDir, "full.txt")
	out, err := runCLI(t, "find", "*", "--workspace", "fx", "--out", dst)
	if err != nil || len(out) > 600 || !strings.HasSuffix(out, "full result: "+dst+"\n") {
		t.Fatalf("%v\n%s", err, out)
	}
	full, err := os.ReadFile(dst)
	if err != nil || len(full) <= len(out) || strings.Contains(string(full), "rerun with") {
		t.Fatalf("%v\n%s", err, full)
	}
}

func TestQueryFlagErrors(t *testing.T) {
	indexed(t)
	if _, err := runCLI(t, "find", "x", "--workspace", "fx", "--format", "yaml"); err == nil {
		t.Fatal("bad format accepted")
	}
	if _, err := runCLI(t, "find", "x", "--workspace", "fx", "--repo", "nope"); err == nil {
		t.Fatal("unknown repo accepted")
	}
	if _, err := runCLI(t, "find", "x", "--workspace", "fx", "--offset", "-1"); err == nil {
		t.Fatal("negative offset accepted")
	}
	if _, err := runCLI(t, "outline", "nowhere/x.go", "--workspace", "fx"); err == nil {
		t.Fatal("outline outside a repo without repo: accepted")
	}
}

func TestQueryFromConfigDirOutsideAnyRepo(t *testing.T) {
	cfgDir := t.TempDir()
	if err := os.CopyFS(filepath.Join(cfgDir, "go-lib"), os.DirFS(filepath.Join(testutil.WorkspaceDir(), "go-lib"))); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(cfgDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("workspaces:\n  fx:\n    repos: [{path: go-lib}]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEDOC_CONFIG", cfgPath)
	t.Setenv("CODEDOC_DATA", filepath.Join(t.TempDir(), "data"))
	t.Chdir(cfgDir)
	if out, err := runCLI(t, "index"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err := runCLI(t, "outline", "go-lib/store")
	if err != nil || !strings.Contains(out, "store/store.go") {
		t.Fatalf("%v\n%s", err, out)
	}
	t.Chdir(filepath.Join(cfgDir, "go-lib", "store"))
	out, err = runCLI(t, "outline", "null.go")
	if err != nil || !strings.Contains(out, "== store/null.go, 8 lines") {
		t.Fatalf("%v\n%s", err, out)
	}
}

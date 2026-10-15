package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/testutil"
)

var update = flag.Bool("update", false, "rewrite the snapshot files under testdata")

// e2eWorkspace writes a config with workspace "fx" holding copies of the
// fixture repos, points codedoc at it, runs index and returns the
// repo copies by name.
func e2eWorkspace(t *testing.T) map[string]string {
	t.Helper()
	repos := map[string]string{}
	var list []string
	for _, name := range []string{"go-lib", "go-app", "ts-domain", "ts-app", "py-lib", "java-lib", "cfg-repo"} {
		repos[name] = testutil.CopyFixture(t, name)
		list = append(list, "{path: "+repos[name]+"}")
	}
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	body := "workspaces:\n  fx:\n    repos: [" + strings.Join(list, ", ") + "]\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEDOC_CONFIG", cfgPath)
	t.Setenv("CODEDOC_DATA", filepath.Join(t.TempDir(), "data"))
	if out, err := runCLI(t, "index", "--workspace", "fx"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	return repos
}

// cliSnapshot compares out with testdata/<name>.txt, or rewrites it with
// -update.
func cliSnapshot(t *testing.T, name, out string) {
	t.Helper()
	p := filepath.Join("testdata", name+".txt")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(out), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("%v (run with -update)", err)
	}
	if string(want) != out {
		t.Errorf("snapshot %s differs:\n--- want\n%s--- got\n%s", name, want, out)
	}
}

func TestE2EQuerySnapshots(t *testing.T) {
	e2eWorkspace(t)
	for name, args := range map[string][]string{
		"outline_store":         {"outline", "go-lib:store/store.go"},
		"outline_module":        {"outline", "github.com/fixture/lib/service"},
		"find_get":              {"find", "Get", "--kind", "method"},
		"symbol_memstore_get":   {"symbol", "MemStore.Get", "--body"},
		"callers_lookup_depth2": {"callers", "Service.Lookup", "--depth", "2"},
		"callers_get_by_file":   {"callers", "MemStore.Get", "--group-by", "file"},
		"callees_lookup":        {"callees", "Service.Lookup"},
		"refs_memstore":         {"refs", "go-lib:store/store.go#MemStore", "--kind", "type"},
		"impls_store":           {"impls", "go-lib:store/store.go#Store"},
		"supertypes_memoryrepo": {"supertypes", "MemoryRepo"},
		"tests_memstore_put":    {"tests", "MemStore.Put"},
		"tests_env_transitive":  {"tests", "Environment", "--transitive"},
		"siblings_memstore_get": {"siblings", "MemStore.Get"},
		"deps_app":              {"deps", "github.com/fixture/app", "--cross-repo"},
		"dependents_store":      {"dependents", "github.com/fixture/lib/store", "--depth", "2", "--cross-repo"},
		"callers_java_put":      {"callers", "java-lib:java:com.fixture.store:MemStore.put#1d09"},
		"callees_py_lookup":     {"callees", "py-lib:py:pylib.service:Service.lookup"},
		"impls_java_store":      {"impls", "java-lib:java:com.fixture.store:Store"},
		"config_lib_mode":       {"config", "LIB_MODE"},
		"config_lib_glob":       {"config", "lib.*", "--defines"},
	} {
		out, err := runCLI(t, append(args, "--workspace", "fx")...)
		if err != nil {
			t.Errorf("%s: %v\n%s", name, err, out)
			continue
		}
		cliSnapshot(t, name, out)
	}
}

func TestE2EExitCodes(t *testing.T) {
	e2eWorkspace(t)
	overloads := []string{"java-lib:java:com.fixture.store:MemStore.put#1d09", "java-lib:java:com.fixture.store:MemStore.put#dbc4"}
	for _, c := range []struct {
		args   []string
		code   int
		stdout []string // each must be in stdout
		stderr string
	}{
		// Nothing at all to offer: one line and exit 1.
		{[]string{"symbol", "Zzyzxq"}, exitFailed, nil, "codedoc: symbol Zzyzxq: not found\n"},
		{[]string{"symbol", "Zzyzxq", "--strict"}, exitFailed, nil, "codedoc: symbol Zzyzxq: not found\n"},
		{[]string{"deps", "nowhere"}, exitFailed, nil, "codedoc: module nowhere: not found\n"},
		// An ambiguous address is answered for the best candidate;
		// --strict lists the candidates and exits 3.
		{[]string{"symbol", "Get", "--no-body"}, 0, []string{"\nalso: MemStore.Get (go-lib:store/store.go:26), "}, ""},
		{[]string{"symbol", "Get", "--strict"}, exitAmbiguous, []string{"== candidates (4)"},
			"codedoc: symbol Get: ambiguous (4 candidates)\n"},
		{[]string{"callers", "Get", "--strict"}, exitAmbiguous, nil, "codedoc: symbol Get: ambiguous (4 candidates)\n"},
		{[]string{"callers", "MemStore.put", "--keys", "--strict"}, exitAmbiguous, overloads,
			"codedoc: symbol MemStore.put: ambiguous (3 candidates)\n"},
		// A find without a match is an answer; a wrong flag value is usage.
		{[]string{"find", "Nope"}, 0, []string{"== no symbol named so; search results: ("}, ""},
		{[]string{"callers", "fallback", "--group-by", "dir"}, exitUsage, nil, "codedoc: --group-by dir: want file\n"},
	} {
		stdout, stderr, code := exitOf(t, append(c.args, "--workspace", "fx")...)
		if code != c.code || stderr != c.stderr {
			t.Errorf("%v: exit %d, stderr %q\n%s", c.args, code, stderr, stdout)
		}
		for _, want := range c.stdout {
			if !strings.Contains(stdout, want) {
				t.Errorf("%v: missing %q in\n%s", c.args, want, stdout)
			}
		}
	}
}

func TestE2EJSONParses(t *testing.T) {
	e2eWorkspace(t)
	out, err := runCLI(t, "callers", "Service.Lookup", "--depth", "2", "--format", "json", "--workspace", "fx")
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Header   struct{ Fresh bool }
		Sections []struct {
			Entries []struct {
				Key   string
				Depth int
				Tags  []string
			}
		}
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	es := resp.Sections[0].Entries
	if !resp.Header.Fresh || len(es) != 2 || es[1].Depth != 1 || es[0].Tags[0] != "import" {
		t.Fatalf("%+v", resp)
	}
}

// moreLine matches the line a budget cut ends a section with.
var moreLine = regexp.MustCompile(`(?m)^(\d+) more: codedoc find '\*' --budget 300 --workspace fx --offset (\d+)$`)

func TestE2EBudgetTruncatesWithTheExactOffset(t *testing.T) {
	e2eWorkspace(t)
	out, err := runCLI(t, "find", "*", "--budget", "300", "--workspace", "fx")
	if err != nil || len(out) > 300 {
		t.Fatalf("%v (%d bytes)\n%s", err, len(out), out)
	}
	total, err := strconv.Atoi(regexp.MustCompile(`== find \S+ \((\d+)\)`).FindStringSubmatch(out)[1])
	if err != nil {
		t.Fatal(err)
	}
	shown := strings.Count(out, "\n  ") // the entries of the one file the page shows
	m := moreLine.FindStringSubmatch(out)
	if m == nil || shown == 0 || m[1] != strconv.Itoa(total-shown) || m[2] != strconv.Itoa(shown) {
		t.Fatalf("total %d shown %d:\n%s", total, shown, out)
	}
	next, err := runCLI(t, "find", "*", "--budget", "300", "--offset", m[2], "--workspace", "fx")
	if err != nil || strings.Contains(next, strings.Split(out, "\n")[3]) {
		t.Fatalf("--offset %s repeats the first page: %v\n%s", m[2], err, next)
	}
}

func TestE2EOutWritesTheFullResult(t *testing.T) {
	e2eWorkspace(t)
	dst := filepath.Join(t.TempDir(), "callers.txt")
	out, err := runCLI(t, "callers", "MemStore.Get", "--out", dst, "--workspace", "fx")
	if err != nil || !strings.HasSuffix(out, "full result: "+dst+"\n") {
		t.Fatalf("%v\n%s", err, out)
	}
	full, err := os.ReadFile(dst)
	if err != nil || !strings.Contains(string(full), "TestMemStorePutGet") {
		t.Fatalf("%v\n%s", err, full)
	}
}

func TestE2EStaleIndexAnswersWithFreshNo(t *testing.T) {
	repos := e2eWorkspace(t)
	t.Setenv("CODEDOC_FRESH_MAX_FILES", "200")
	dir := filepath.Join(repos["go-lib"], "gen")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 201; i++ {
		src := fmt.Sprintf("package gen\n\nfunc G%d() {}\n", i)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("g%d.go", i)), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, err := runCLI(t, "callers", "Service.Lookup", "--workspace", "fx")
	if err != nil || !regexp.MustCompile(`^go-lib@\w+ go-app@\w+ dirty fresh:no\(go-lib\)\n`).MatchString(out) ||
		!strings.Contains(out, "index is stale (201 files changed); run codedoc index") {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "\nrun  go-app:main.go:16  ") {
		t.Fatalf("a stale answer is still an answer:\n%s", out)
	}
}

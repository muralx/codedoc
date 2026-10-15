package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/docs/write"
	"github.com/muralx/codedoc/internal/query"
	"github.com/muralx/codedoc/internal/session"
)

// runSplit runs codedoc as main does, with stdin, and returns what it
// wrote to stdout and to stderr (the error line included) apart.
func runSplit(t *testing.T, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	root := newRootCmd()
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(args)
	cmd, err := root.ExecuteC()
	if err != nil {
		reportError(&out, &errOut, cmd, err)
	}
	return out.String(), errOut.String(), err
}

// silent fails the test unless the command succeeds without a byte on
// stdout or stderr.
func silent(t *testing.T, stdin string, args ...string) {
	t.Helper()
	if stdout, stderr, err := runSplit(t, stdin, args...); err != nil || stdout != "" || stderr != "" {
		t.Errorf("%v: err=%v stdout=%q stderr=%q", args, err, stdout, stderr)
	}
}

// verbose fails the test unless the command, run with --verbose,
// succeeds, prints every want on stdout and nothing on stderr.
func verbose(t *testing.T, stdin string, args []string, want ...string) {
	t.Helper()
	stdout, stderr, err := runSplit(t, stdin, append(args, "--verbose")...)
	if err != nil || stderr != "" {
		t.Errorf("%v --verbose: err=%v stderr=%q", args, err, stderr)
	}
	for _, w := range want {
		if !strings.Contains(stdout, w) {
			t.Errorf("%v --verbose: missing %q in\n%s", args, w, stdout)
		}
	}
}

// oneLine fails the test unless the command fails with status 1,
// nothing on stdout and exactly the line want on stderr.
func oneLine(t *testing.T, stdin string, args []string, want string) {
	t.Helper()
	stdout, stderr, err := runSplit(t, stdin, args...)
	if err == nil || stdout != "" || stderr != want+"\n" {
		t.Errorf("%v: err=%v stdout=%q stderr=%q", args, err, stdout, stderr)
	}
}

func TestIndexAndDepsWritesAreSilent(t *testing.T) {
	depsWorkspace(t, "")
	silent(t, "", "index", "--workspace", "fx")
	silent(t, "", "index", "--workspace", "fx", "--rebuild")
	silent(t, "", "deps", "index", "--workspace", "fx")
	silent(t, "", "deps", "prune")
	stdout, stderr, err := runSplit(t, "", "deps", "prune", "--all", "--dry-run")
	if err != nil || stderr != "" || !strings.Contains(stdout, "== would prune (6)") {
		t.Errorf("a dry run is a question and prints its list: %v %q\n%s", err, stderr, stdout)
	}
	silent(t, "", "deps", "prune", "--all")
	verbose(t, "", []string{"deps", "prune"}, "== pruned (0)")
	verbose(t, "", []string{"deps", "index", "--workspace", "fx"}, "== deps (", "indexed 6 package version(s)")
	verbose(t, "", []string{"index", "--workspace", "fx"}, "== index (2)", "deps-go  main@", "== deps (2)")
	stdout, stderr, err = runSplit(t, "", "index", "--workspace", "fx", "--format", "json")
	if err != nil || stderr != "" || !strings.Contains(stdout, `"title":"index"`) {
		t.Errorf("--format json keeps the result: %v %q\n%s", err, stderr, stdout)
	}
}

// What a dependency sync could not do is not in a silent run's output,
// so the run warns in one line; --verbose shows it in the note.
func TestSilentIndexWarnsAboutWhatTheSyncLeft(t *testing.T) {
	repos := depsWorkspace(t, "")
	if err := os.WriteFile(filepath.Join(repos["deps-app"], "package.json"), []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"index", "--workspace", "fx"}, {"deps", "index", "--workspace", "fx"}} {
		stdout, stderr, err := runSplit(t, "", args...)
		if err != nil || stdout != "" || strings.Count(stderr, "\n") != 1 || !strings.HasPrefix(stderr, "warning: ") ||
			!strings.Contains(stderr, "unreadable manifest ") || !strings.HasSuffix(stderr, "; see them with codedoc deps\n") {
			t.Errorf("%v: err=%v stdout=%q stderr=%q", args, err, stdout, stderr)
		}
	}
	stdout, stderr, err := runSplit(t, "", "deps", "index", "--workspace", "fx", "--verbose")
	if err != nil || stderr != "" || !strings.Contains(stdout, "unreadable manifest ") {
		t.Errorf("--verbose: %v %q\n%s", err, stderr, stdout)
	}
}

// A repo that cannot be indexed is one line; the others are indexed and
// the exit status is 1.
func TestIndexReportsAFailedRepoInOneLine(t *testing.T) {
	_, goLib := setupWorkspace(t)
	if err := os.RemoveAll(goLib); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := runSplit(t, "", "index", "--workspace", "fx")
	if !errors.Is(err, query.ErrReported) || exitCode(err, false) != 1 || stdout != "" || strings.Count(stderr, "\n") != 1 ||
		!strings.HasPrefix(stderr, "go-lib: ") ||
		!strings.HasSuffix(stderr, "; fix that and run codedoc index --workspace fx --repo go-lib\n") {
		t.Fatalf("err=%v stdout=%q stderr=%q", err, stdout, stderr)
	}
	if out := cli(t, "status", "--workspace", "fx", "--repo", "ts-domain"); strings.Contains(out, "fresh:no") ||
		!strings.Contains(out, "ts-domain  main@") {
		t.Errorf("the other repo is indexed:\n%s", out)
	}
}

// With a failed repo --verbose and --format json still print what was
// done, and scip run, which needs every repo, stops.
func TestIndexWithAFailedRepoStillPrintsWhatWasAsked(t *testing.T) {
	_, goLib := setupWorkspace(t)
	if err := os.RemoveAll(goLib); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := runSplit(t, "", "index", "--workspace", "fx", "--verbose")
	if err == nil || !strings.Contains(stdout, "== index (1)\nts-domain  main@") || !strings.HasPrefix(stderr, "go-lib: ") {
		t.Errorf("--verbose: err=%v stdout=%q stderr=%q", err, stdout, stderr)
	}
	stdout, stderr, err = runSplit(t, "", "index", "--workspace", "fx", "--repo", "go-lib", "--format", "json")
	if err == nil || stdout != `{"error":"1 of 1 repos failed: the failures are listed on stderr","code":1}`+"\n" ||
		strings.Count(stderr, "\n") != 1 {
		t.Errorf("json, every repo failed: err=%v stdout=%q stderr=%q", err, stdout, stderr)
	}
	if _, err := runCLI(t, "scip", "run", "--workspace", "fx"); err == nil || !strings.HasPrefix(err.Error(), "index go-lib: ") {
		t.Errorf("scip run needs every repo indexed: %v", err)
	}
}

func TestDocSetAndFeedbackAreSilent(t *testing.T) {
	repos := e2eWorkspace(t)
	file := feedbackState(t)
	silent(t, "Get returns the value under key.\n", "doc", "set", "go-lib:store/store.go#MemStore.Get", "--workspace", "fx")
	silent(t, "", "doc", "set", "go-lib:service/", "--text", "Package service runs use cases.", "--workspace", "fx")
	src, _ := os.ReadFile(filepath.Join(repos["go-lib"], "store", "store.go"))
	if !strings.Contains(string(src), "// Get returns the value under key.\nfunc (m *MemStore) Get") {
		t.Fatalf("the doc was not written:\n%s", src)
	}
	verbose(t, "", []string{"doc", "set", "MemStore.Put", "--text", "Put stores value under key.", "--workspace", "fx"},
		"== doc set (1)", "MemStore.Put  store/store.go:", "doc ok  Put stores value under key.")
	stdout, stderr, err := runSplit(t, "", "doc", "set", "MemStore.Put", "--text", "Put stores it.", "--workspace", "fx", "--format", "json")
	if err != nil || stderr != "" || !strings.Contains(stdout, `"state":"ok"`) {
		t.Errorf("--format json keeps the result: %v %q\n%s", err, stderr, stdout)
	}
	out := filepath.Join(t.TempDir(), "set.txt")
	silent(t, "", "doc", "set", "MemStore.Put", "--text", "Put stores value under key.", "--workspace", "fx", "--out", out)
	if raw, _ := os.ReadFile(out); !strings.Contains(string(raw), "== doc set (1)") {
		t.Errorf("--out still gets the result: %q", raw)
	}
	silent(t, "", "feedback", "refs misses a caller", "--workspace", "fx", "--repo", "go-lib")
	if raw, _ := os.ReadFile(file); !strings.Contains(string(raw), `"text":"refs misses a caller"`) {
		t.Errorf("the report was not filed: %q", raw)
	}
	verbose(t, "", []string{"feedback", "again", "--workspace", "fx"}, "feedback recorded in "+file+"\n")
	silent(t, "", "feedback", "--clear")
	verbose(t, "", []string{"feedback", "--clear"}, "feedback cleared (0 reports)\n")
}

func TestDocSetFailuresAreOneLine(t *testing.T) {
	e2eWorkspace(t)
	ws := []string{"--workspace", "fx", "--repo", "go-lib", "--text", "A doc."}
	oneLine(t, "", append([]string{"doc", "set", "Nope"}, ws...),
		"codedoc: doc set Nope: not found; look its name up with codedoc find, or its file with codedoc outline")
	libStore := "go-lib:go:github.com/fixture/lib/store:"
	oneLine(t, "", append([]string{"doc", "set", "Put"}, ws...), "codedoc: doc set Put: ambiguous (3 candidates); "+
		"address one by its key: "+libStore+"NullStore.Put, "+libStore+"Store.Put, "+libStore+"MemStore.Put")
	oneLine(t, "", []string{"doc", "set", "--workspace", "fx"},
		"codedoc: doc set: no address (give a symbol or module, or --batch with records on stdin)")
	oneLine(t, "", []string{"feedback"}, "codedoc: feedback needs a text, or --list or --clear")
	stdout, stderr, err := runSplit(t, "", append([]string{"doc", "set", "Put", "--verbose"}, ws...)...)
	if !errors.Is(err, query.ErrAmbiguous) || !strings.Contains(stdout, "== candidates (3)") || strings.Count(stderr, "\n") != 1 {
		t.Errorf("--verbose lists the candidates: err=%v stdout=%q stderr=%q", err, stdout, stderr)
	}
}

const batchDocs = `=== go-lib:store/store.go#MemStore.Get
Get returns the value stored under key.

It returns ErrMissing when the key has no value.

=== go-lib:store/store.go#MemStore.Put
Put stores value under key.
=== go-lib:service/
Package service runs the fixture use cases.
=== ts-domain:src/records.ts#MemoryRepo
Keeps environments in memory.
`

func TestDocSetBatch(t *testing.T) {
	repos := e2eWorkspace(t)
	silent(t, batchDocs, "doc", "set", "--batch", "--workspace", "fx")
	src, _ := os.ReadFile(filepath.Join(repos["go-lib"], "store", "store.go"))
	for _, want := range []string{"// Get returns the value stored under key.\n//\n// It returns ErrMissing when the key has no value.\nfunc (m *MemStore) Get",
		"// Put stores value under key.\nfunc (m *MemStore) Put"} {
		if !strings.Contains(string(src), want) {
			t.Errorf("missing %q in\n%s", want, src)
		}
	}
	out := cli(t, "symbol", "ts-domain:src/records.ts#MemoryRepo", "--doc-only", "--workspace", "fx")
	if !strings.Contains(out, "implements EnvironmentRepo  Keeps environments in memory.\n") {
		t.Errorf("the index has the new doc:\n%s", out)
	}
	verbose(t, batchDocs, []string{"doc", "set", "--batch", "--workspace", "fx"}, "== doc set (4)",
		"MemStore.Get  :25-27  doc ok  Get returns the value stored under key.",
		"github.com/fixture/lib/service  service/service.go:1  doc ok")
	stdout, stderr, err := runSplit(t, batchDocs, "doc", "set", "--batch", "--workspace", "fx", "--format", "json")
	if err != nil || stderr != "" || strings.Count(stdout, `"state":"ok"`) != 4 {
		t.Errorf("--format json lists the docs written: %v %q\n%s", err, stderr, stdout)
	}
}

func TestDocSetBatchReportsEachFailureAndWritesTheRest(t *testing.T) {
	repos := e2eWorkspace(t)
	in := "=== Nope\nA doc.\n=== go-lib:store/store.go#MemStore.Put\nPut stores value under key.\n=== Put\nA doc.\n=== NoText\n"
	stdout, stderr, err := runSplit(t, in, "doc", "set", "--batch", "--workspace", "fx", "--repo", "go-lib")
	libStore := "go-lib:go:github.com/fixture/lib/store:"
	want := `NoText: no doc text; write it on the lines after "=== NoText"` + "\n" +
		"Nope: not found; look its name up with codedoc find, or its file with codedoc outline\n" +
		"Put: ambiguous (3 candidates); address one by its key: " + libStore + "NullStore.Put, " + libStore + "Store.Put, " +
		libStore + "MemStore.Put\n"
	if !errors.Is(err, query.ErrReported) || exitCode(err, false) != 1 || stdout != "" || stderr != want {
		t.Fatalf("err=%v stdout=%q stderr:\n%s", err, stdout, stderr)
	}
	if src, _ := os.ReadFile(filepath.Join(repos["go-lib"], "store", "store.go")); !strings.Contains(string(src), "// Put stores value under key.") {
		t.Errorf("the record that could be written was not:\n%s", src)
	}
	stdout, stderr, err = runSplit(t, in, "doc", "set", "--batch", "--workspace", "fx", "--repo", "go-lib", "--format", "json")
	if err == nil || stderr != want || strings.Count(stdout, `"state":"ok"`) != 1 ||
		!strings.HasSuffix(stdout, `{"error":"3 of 4 docs failed: the failures are listed on stderr","code":1}`+"\n") {
		t.Errorf("json: err=%v stdout=%q stderr=%q", err, stdout, stderr)
	}
}

func TestDocSetBatchRefusesMalformedInput(t *testing.T) {
	e2eWorkspace(t)
	batch := []string{"doc", "set", "--batch", "--workspace", "fx"}
	for _, stdin := range []string{"", "\n \n"} {
		oneLine(t, stdin, batch, `codedoc: doc set --batch: no records on stdin; write a line "=== <address>" and the doc text below it, once per doc`)
	}
	oneLine(t, "MemStore.Get\nGet returns the value.\n", batch,
		`line 1: text before the first "=== <address>" line; start every record with one`)
	oneLine(t, "=== \nGet returns the value.\n", batch, `line 1: a record without an address; write "=== <address>"`)
	oneLine(t, batchDocs, append(batch, "MemStore.Get"),
		`codedoc: doc set --batch: the addresses come from stdin ("=== <address>" lines), not from an argument`)
	if _, stderr, err := runSplit(t, batchDocs, append(batch, "--text", "x")...); err == nil || !strings.Contains(stderr, "[batch text]") {
		t.Errorf("--batch excludes --text: %v %q", err, stderr)
	}
}

// A batch under CODEDOC_RUN_ID journals every file it writes, as single
// doc set calls do, so the run can be reverted.
func TestDocSetBatchJournalsItsWrites(t *testing.T) {
	repos := e2eWorkspace(t)
	feedbackState(t) // a private state directory for the journal
	files := []string{filepath.Join(repos["go-lib"], "service", "service.go"), filepath.Join(repos["go-lib"], "store", "store.go"),
		filepath.Join(repos["ts-domain"], "src", "records.ts")}
	before := map[string]string{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		before[f] = string(raw)
	}
	t.Setenv(write.RunIDEnv, "batch-run")
	// One record fails: its line is on stderr and the rest is journaled.
	_, stderr, err := runSplit(t, batchDocs+"=== Nope\nA doc.\n=== go-lib:store/null.go#NullStore.Get\nGet finds nothing.\n",
		"doc", "set", "--batch", "--workspace", "fx")
	if !errors.Is(err, query.ErrReported) || !strings.HasPrefix(stderr, "Nope: not found; ") || strings.Count(stderr, "\n") != 1 {
		t.Fatalf("err=%v stderr=%q", err, stderr)
	}
	journaled, err := write.Files("batch-run")
	want := []string{files[0], filepath.Join(repos["go-lib"], "store", "null.go"), files[1], files[2]}
	if err != nil || !slices.Equal(journaled, want) {
		t.Fatalf("journal: %v\n%s", err, strings.Join(journaled, "\n"))
	}
	if kept, err := write.Restore("batch-run", false); err != nil || len(kept) != 0 {
		t.Fatalf("restore: %v %v", err, kept)
	}
	for _, f := range files {
		if raw, _ := os.ReadFile(f); string(raw) != before[f] {
			t.Errorf("%s was not reverted", f)
		}
	}
}

func TestMCPDocSetAnswersOkAndTakesABatch(t *testing.T) {
	repos := e2eWorkspace(t)
	t.Setenv(session.HeadlessEnv, "1")
	cs, _ := mcpSession(t)
	got, isErr := mcpCall(t, cs, "codedoc_doc", map[string]any{"action": "set", "workspace": "fx",
		"target": "go-lib:service/", "text": "Package service runs use cases."})
	if isErr || got != "ok" {
		t.Fatalf("single: %v %q", isErr, got)
	}
	docs := []map[string]any{
		{"address": "go-lib:store/store.go#MemStore.Get", "text": "Get returns the value stored under key.\n\nIt returns ErrMissing otherwise."},
		{"address": "go-lib:store/store.go#MemStore.Put", "text": "Put stores value under key."},
	}
	if got, isErr = mcpCall(t, cs, "codedoc_doc", map[string]any{"action": "set", "workspace": "fx", "docs": docs}); isErr || got != "ok" {
		t.Fatalf("batch: %v %q", isErr, got)
	}
	src, _ := os.ReadFile(filepath.Join(repos["go-lib"], "store", "store.go"))
	if !strings.Contains(string(src), "// It returns ErrMissing otherwise.\nfunc (m *MemStore) Get") ||
		!strings.Contains(string(src), "// Put stores value under key.\nfunc (m *MemStore) Put") {
		t.Errorf("the batch was not written:\n%s", src)
	}
	docs = append(docs, map[string]any{"address": "Nope", "text": "A doc."})
	got, isErr = mcpCall(t, cs, "codedoc_doc", map[string]any{"action": "set", "workspace": "fx", "docs": docs})
	if !isErr || got != "Nope: not found; look its name up with codedoc find, or its file with codedoc outline\n" {
		t.Errorf("a failed doc is its line alone: %v %q", isErr, got)
	}
}

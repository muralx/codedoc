package query_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/query"
)

func TestParseDocBatch(t *testing.T) {
	recs, failed := query.ParseDocBatch("\n=== a.go#A  \r\n\n  \nA does it.\r\n\nMore of A.\n\n\n=== r:dir/\nPackage dir.\n===\nnot a mark\n")
	want := []query.DocRecord{{Addr: "a.go#A", Text: "A does it.\n\nMore of A."}, {Addr: "r:dir/", Text: "Package dir.\n===\nnot a mark"}}
	if !slices.Equal(recs, want) || len(failed) != 0 {
		t.Fatalf("records %q, failed %q", recs, failed)
	}
	if recs, failed := query.ParseDocBatch(" \n\n"); len(recs)+len(failed) != 0 {
		t.Fatalf("blank input: %q %q", recs, failed)
	}
	recs, failed = query.ParseDocBatch("stray\nmore stray\n=== \ntext of nobody\n=== B\n\n=== C\nC does it.")
	if !slices.Equal(recs, []query.DocRecord{{Addr: "C", Text: "C does it."}}) {
		t.Fatalf("records %q", recs)
	}
	wantFailed := []string{
		`line 1: text before the first "=== <address>" line; start every record with one`,
		`line 3: a record without an address; write "=== <address>"`,
		`B: no doc text; write it on the lines after "=== B"`,
	}
	if !slices.Equal(failed, wantFailed) {
		t.Fatalf("failed:\n%s", strings.Join(failed, "\n"))
	}
}

// A batch writes symbols of one file, a module and a symbol of another
// repo, and checks and re-indexes each repo once.
func TestDocSetBatchWritesEveryRecord(t *testing.T) {
	c := workspaceOf(t, "go-lib", "ts-domain")
	checks := map[string]int{}
	c.DaemonFresh = func(_ string, r *config.Repo) bool { checks[r.Name]++; return false }
	recs := []query.DocRecord{
		{Addr: "go-lib:store/store.go#MemStore.Get", Text: "Get returns the value stored under key.\n\nIt returns ErrMissing otherwise."},
		{Addr: libStore + "MemStore.Put", Text: "Put stores value under key."},
		{Addr: "go-lib:service/", Text: "Package service runs the fixture use cases."},
		{Addr: records + "MemoryRepo", Text: "Keeps environments in memory."},
	}
	resp, failed, err := query.DocSetBatch(c, recs)
	if len(failed) != 0 {
		t.Fatalf("failed: %q", failed)
	}
	mustContain(t, run(t, resp, err), "== doc set (4)",
		"go-lib:store/store.go\n  "+libStore+"MemStore.Get  :25-27  doc ok  Get returns the value stored under key.",
		"  "+libStore+"MemStore.Put  :36  doc ok  Put stores value under key.",
		"github.com/fixture/lib/service  service/service.go:1  doc ok", "doc ok  Keeps environments in memory.")
	src := readRepoFile(t, c, "go-lib", "store/store.go")
	mustContain(t, src, "// Get returns the value stored under key.\n//\n// It returns ErrMissing otherwise.\nfunc (m *MemStore) Get",
		"// Put stores value under key.\nfunc (m *MemStore) Put")
	mustContain(t, readRepoFile(t, c, "ts-domain", "src/records.ts"), "/** Keeps environments in memory. */\nexport class MemoryRepo")
	for _, key := range []string{libStore + "MemStore.Get", libStore + "MemStore.Put", records + "MemoryRepo"} {
		if got := symbolState(t, c, key); got != "ok" {
			t.Errorf("%s: doc %s", key, got)
		}
	}
	if checks["go-lib"] != 2 || checks["ts-domain"] != 2 {
		t.Errorf("each repo is checked once before the writes and once after its re-index: %v", checks)
	}
}

// Records that cannot be written are one line each; the others are
// written all the same.
func TestDocSetBatchReportsFailuresAndWritesTheRest(t *testing.T) {
	c := ambiguousWorkspace(t)
	resp, failed, err := query.DocSetBatch(c, []query.DocRecord{
		{Addr: "Nope", Text: "x"},
		{Addr: "Get", Text: "x"},
		{Addr: libStore + "MemStore.Put", Text: "Put stores value under key."},
		{Addr: "go-lib:nope/", Text: "x"},
		{Addr: records + "MemoryRepo", Text: "Ends the comment */ early."},
	})
	mustContain(t, run(t, resp, err), "== doc set (1)", "doc ok  Put stores value under key.")
	want := []string{
		"Nope: not found; look its name up with codedoc find, or its file with codedoc outline",
		"Get: ambiguous (4 candidates); address one by its key: " + libService + "Cache.Get, " + libStore + "NullStore.Get, " +
			libStore + "Store.Get (and 1 more: codedoc find lists them)",
		"go-lib:nope/: not found; look its name up with codedoc find, or its file with codedoc outline",
		records + "MemoryRepo: doc text for src/records.ts/MemoryRepo contains */; nothing was written for it: fix that and set it again",
	}
	if !slices.Equal(failed, want) {
		t.Fatalf("failed:\n%s", strings.Join(failed, "\n"))
	}
	if strings.Contains(readRepoFile(t, c, "ts-domain", "src/records.ts"), "Ends the comment") {
		t.Error("a refused doc was written")
	}
}

func TestDocSetBatchWithNothingWrittenHasNoResponse(t *testing.T) {
	c := workspaceOf(t, "go-lib")
	resp, failed, err := query.DocSetBatch(c, []query.DocRecord{{Addr: "Nope", Text: "x"}})
	if resp != nil || err != nil || len(failed) != 1 {
		t.Fatalf("%+v %v %q", resp, err, failed)
	}
}

// A failed doc set says what failed and what to do in one line, and
// keeps the cause for the exit status.
func TestDocSetErrorsSayWhatToDo(t *testing.T) {
	c := tempWorkspace(t, map[string]string{
		"go.mod":  "module x/m\n",
		"gen.go":  "// Code generated by x. DO NOT EDIT.\n\npackage m\n\nfunc Gen() {}\n",
		"a.go":    "package m\n\nfunc A() {}\n",
		"b/a.go":  "package b\n\nfunc A() {}\n",
		"docs.md": "# Title\n\ntext\n",
	})
	for addr, want := range map[string]string{
		"Gen":           "doc set Gen: is in a generated file; document the source it is generated from",
		"Nope":          "doc set Nope: not found; look its name up with codedoc find, or its file with codedoc outline",
		"A":             "doc set A: ambiguous (2 candidates); address one by its key: r:go:x/m:A, r:go:x/m/b:A",
		"docs.md#Title": "doc set docs.md#Title: is a section of docs.md, which has no doc to set; edit the file",
	} {
		_, err := query.DocSet(c, addr, "A doc.")
		var de *query.DocSetError
		if err == nil || err.Error() != want || !errors.As(err, &de) || strings.Contains(err.Error(), "\n") {
			t.Errorf("%s: %v", addr, err)
		}
	}
	if _, err := query.DocSet(c, "Nope", "x"); !errors.Is(err, query.ErrNotFound) {
		t.Errorf("the cause is kept: %v", err)
	}
	if _, err := query.DocSet(c, "A", "x"); !errors.Is(err, query.ErrAmbiguous) {
		t.Errorf("the cause is kept: %v", err)
	}
}

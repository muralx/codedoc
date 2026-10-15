package query_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/muralx/codedoc/internal/feedback"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/query"
	"github.com/muralx/codedoc/internal/store"
)

func TestLinesPrintsTheRangeUnderItsOwners(t *testing.T) {
	c := docWorkspace(t)
	snapshot(t, "lines_store", out(t, must(query.Lines(c, "go-lib:store/store.go", "24-28"))))
	for rng, want := range map[string]string{
		"26-32": "store/store.go:26-32  in MemStore.Get (26-32)",
		"11+1":  "store/store.go:11-12  in Store.Get (12)",
		"12":    "store/store.go:12  in Store.Get (12)\n12\t\tGet(key string) (string, error)\n",
		"1-3":   "store/store.go:1-3  outside any indexed symbol",
		"30-99": "store/store.go:30-38  in MemStore.Get (26-32) .. MemStore.Put (35-38)",
		// The innermost symbol that encloses the whole range, not the one
		// of some line of it.
		"10-12": "store/store.go:10-12  in Store (10-15)\n10\ttype Store interface {\n",
		"9-15":  "store/store.go:9-15  in Store (10-15)\n9\t// Store persists",
		"18-19": "store/store.go:18-19  in MemStore (18-20)",
		// No symbol encloses it: the ones it starts and ends in.
		"12-19": "store/store.go:12-19  in Store.Get (12) .. MemStore.data (19)\n",
		"1+900": "store/store.go:1-38  in ErrMissing (7) .. MemStore.Put (35-38)\n",
	} {
		if text := out(t, must(query.Lines(c, "go-lib:store/store.go", rng))); !strings.Contains(text, want) {
			t.Errorf("%s: missing %q in\n%s", rng, want, text)
		}
	}
	// The answer is one header line and the lines: no title, and a note
	// only when the index and the file disagree.
	text := out(t, must(query.Lines(c, "go-lib:store/store.go", "26-32")))
	if want := "go-lib@0b29eeb\nstore/store.go:26-32  in MemStore.Get (26-32)\n26\tfunc (m *MemStore) Get("; !strings.HasPrefix(text, want) ||
		!strings.HasSuffix(text, "\n32\t}\n") {
		t.Errorf("want %q and the lines alone:\n%s", want, text)
	}
	text = out(t, must(query.Lines(c, "doc-repo:docs/design.md", "48-52")))
	mustContain(t, text, "docs/design.md:48-52  in 6. Delivery > 6.2 Retries (46-53)\n48\tA failed delivery")
	text = out(t, must(query.Lines(c, "doc-repo:docs/CHANGES.txt", "1-2")))
	mustContain(t, text, "docs/CHANGES.txt:1-2  in CHANGES.txt (1-6)")
}

func TestLinesRejectsBadRangesAndUnknownFiles(t *testing.T) {
	c := docWorkspace(t)
	for _, rng := range []string{"", "a-b", "0-3", "9-3", "3-", "-3", "3+x"} {
		if _, err := query.Lines(c, "go-lib:store/store.go", rng); err == nil {
			t.Errorf("range %q accepted", rng)
		}
	}
	if _, err := query.Lines(c, "go-lib:store/store.go", "39-40"); err == nil || !strings.Contains(err.Error(), "past the end (38 lines)") {
		t.Errorf("past the end: %v", err)
	}
	resp, err := query.Lines(c, "go-lib:store/absent.go", "1-2")
	if !errors.Is(err, query.ErrNotFound) || errors.Is(err, query.ErrNearest) || resp != nil ||
		!strings.Contains(err.Error(), "lines go-lib:store/absent.go: not found: no such file in go-lib") {
		t.Errorf("unknown file: %v", err)
	}
	if _, err := query.Lines(c, "store/store.go", "1-2"); err == nil || !strings.Contains(err.Error(), "write repo:path") {
		t.Errorf("outside a repo: %v", err)
	}
}

func TestLinesOfAFileTheIndexLacks(t *testing.T) {
	c := docWorkspace(t)
	root := c.Workspace.Repos[1].Path
	if err := os.WriteFile(filepath.Join(root, "notes.bin"), []byte("one\ntwo\r\nthree"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.Workspace.Exclude = []string{"*.bin"}
	text := out(t, must(query.Lines(c, "go-lib:notes.bin", "2-3")))
	mustContain(t, text, "notes.bin:2-3  outside any indexed symbol\n2\ttwo\n3\tthree\n", "notes.bin is not indexed: it matches the exclude glob *.bin")
}

func TestLinesReindexesAChangedFileAndReportsMismatches(t *testing.T) {
	c := docWorkspace(t)
	root := c.Workspace.Repos[1].Path
	// The freshness check re-extracts nothing, so the edit below leaves
	// the index behind until lines re-indexes the file itself.
	t.Setenv("CODEDOC_FRESH_MAX_FILES", "0")
	editRepo(t, root, "store/store.go", "// MemStore is an in-memory Store.", "// One more line.\n// MemStore is an in-memory Store.")
	text := out(t, must(query.Lines(c, "go-lib:store/store.go", "27-33")))
	mustContain(t, text, "store/store.go:27-33  in MemStore.Get (27-33)", "store/store.go changed since it was indexed: re-indexed before answering")
	if strings.Contains(text, "index mismatch") {
		t.Errorf("a re-indexed file matches:\n%s", text)
	}
	// An index that disagrees with the file although their hashes match
	// (a wrong span recorded by an extractor) is reported.
	key := "go-lib:go:github.com/fixture/lib/store:MemStore.Get"
	sy, err := c.Store.SymbolByKey(key)
	if err != nil {
		t.Fatal(err)
	}
	shifted := sy
	shifted.StartLine, shifted.SigStartLine, shifted.SigEndLine, shifted.EndLine = 30, 30, 30, 36
	err = c.Store.Batch(func(b *store.Batch) error {
		_, err := b.SyncFileSymbols(sy.FileID, sy.ModuleID, []store.Symbol{shifted})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	text = out(t, must(query.Lines(c, "go-lib:store/store.go", "27-33")))
	mustContain(t, text, "index mismatch: "+key+" recorded 30-36; report it with codedoc feedback")
	shifted.StartLine, shifted.SigStartLine, shifted.SigEndLine, shifted.EndLine = 30, 30, 30, 99
	_ = c.Store.Batch(func(b *store.Batch) error {
		_, err := b.SyncFileSymbols(sy.FileID, sy.ModuleID, []store.Symbol{shifted})
		return err
	})
	if text := out(t, must(query.Lines(c, "go-lib:store/store.go", "30"))); !strings.Contains(text, "recorded 30-99") {
		t.Errorf("a range past the end of the file is a mismatch:\n%s", text)
	}
}

func TestFeedbackEntryDescribesTheIndexState(t *testing.T) {
	c := docWorkspace(t)
	c.Now = func() time.Time { return time.Date(2026, 3, 3, 12, 0, 0, 0, time.UTC) }
	key := "go-lib:go:github.com/fixture/lib/store:MemStore.Get"
	e := query.FeedbackEntry(c, "body is not the function", query.FeedbackOpts{Key: key, Cmd: "codedoc symbol MemStore.Get --body"})
	hash := e.IndexedHash
	want := feedback.Entry{Time: "2026-03-03T12:00:00Z", Workspace: "fixture", Repo: "go-lib", Head: "main@0b29eeb",
		Build: e.Build, Extractor: index.ExtractorVersion(), Text: "body is not the function", Key: key,
		Cmd: "codedoc symbol MemStore.Get --body", IndexedHash: hash, CurrentHash: hash}
	if e != want || hash == "" || e.Build == "" {
		t.Errorf("entry = %+v", e)
	}
	// A report is about the index as it is: the file is not re-indexed,
	// so its hashes tell that the index is behind.
	editRepo(t, c.Workspace.Repos[1].Path, "store/store.go", "// MemStore is", "// MemStore was")
	e = query.FeedbackEntry(c, "stale", query.FeedbackOpts{Path: "go-lib:store/store.go"})
	if !e.Dirty || e.IndexedHash != hash || e.CurrentHash == hash || e.Path != "go-lib:store/store.go" {
		t.Errorf("after an edit: %+v", e)
	}
}

func TestFeedbackEntryWithoutAFile(t *testing.T) {
	c := docWorkspace(t)
	for name, o := range map[string]query.FeedbackOpts{"unknown key": {Key: "go-lib:go:x:Nope"}, "unknown repo": {Key: "zz:go:x:Nope"},
		"unknown path": {Path: "nowhere/x.go"}, "nothing": {}} {
		if e := query.FeedbackEntry(c, "t", o); e.IndexedHash+e.CurrentHash != "" || e.Text != "t" {
			t.Errorf("%s: %+v", name, e)
		}
	}
	c.Repo = c.Workspace.Repos[0]
	if e := query.FeedbackEntry(c, "t", query.FeedbackOpts{}); e.Repo != "doc-repo" || !strings.HasPrefix(e.Head, "main@") {
		t.Errorf("the current repo: %+v", e)
	}
}

func TestTimeBound(t *testing.T) {
	now := time.Date(2026, 3, 3, 12, 0, 0, 0, time.UTC)
	for v, want := range map[string]string{"30m": "2026-03-03T11:30:00Z", "3h": "2026-03-03T09:00:00Z", "2d": "2026-03-01T12:00:00Z",
		"1w": "2026-02-24T12:00:00Z", "2026-03-01": "2026-03-01T00:00:00Z", "2026-03-01T10:30": "2026-03-01T10:30:00Z"} {
		if got, ok := query.TimeBound(v, now); !ok || got.UTC().Format(time.RFC3339) != want {
			t.Errorf("%s = %v %v, want %s", v, got, ok, want)
		}
	}
	for _, v := range []string{"", "3", "3y", "yesterday", "HEAD"} {
		if _, ok := query.TimeBound(v, now); ok {
			t.Errorf("%q accepted", v)
		}
	}
}

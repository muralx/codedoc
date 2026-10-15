package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/muralx/codedoc/internal/session"
	"github.com/muralx/codedoc/internal/store"
)

// script writes an executable shell script and returns its path.
func script(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "stub")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// scipCommit returns the commit go-lib's SCIP index was merged from.
func (h *harness) scipCommit() string {
	h.t.Helper()
	s, err := store.Open(h.cfg.DBPath("fx"))
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	row, err := s.RepoByPath(h.repos["go-lib"])
	if err != nil {
		h.t.Fatal(err)
	}
	return row.SCIPCommit
}

func TestScheduledSCIPRunsAtOnceThenEveryInterval(t *testing.T) {
	fixture, err := filepath.Abs("../../scip/testdata/go-lib.scip")
	if err != nil {
		t.Fatal(err)
	}
	runs := filepath.Join(t.TempDir(), "runs")
	t.Setenv("CODEDOC_SCIP_GO", script(t, `echo run >> '`+runs+`'; cp '`+fixture+`' "$2"`))
	t.Setenv("CODEDOC_SCIP_TS", filepath.Join(t.TempDir(), "no-scip-typescript"))
	h := newHarness(t, "    scip: {enabled: [go-lib, ts-domain], every: 6h}\n", "go-lib", "ts-domain")
	h.start(h.options())
	h.eventually("the first SCIP merge", waitLimit, func() bool { return h.scipCommit() != "" })
	count := func() int { b, _ := os.ReadFile(runs); return strings.Count(string(b), "run") }
	if st := h.repoStatus("go-lib"); !st.NextSCIP.Equal(h.clock.Now().Add(6 * time.Hour)) {
		t.Fatalf("next scip %v", st.NextSCIP)
	}
	h.eventually("the missing indexer logged", waitLimit, func() bool {
		return strings.Contains(h.log.String(), "fx/ts-domain: scip ts: missing indexer")
	})
	h.clock.advance(5 * time.Hour)
	time.Sleep(100 * time.Millisecond)
	if n := count(); n != 1 {
		t.Fatalf("%d runs before the interval", n)
	}
	h.clock.advance(time.Hour)
	h.eventually("the second SCIP run", waitLimit, func() bool { return count() == 2 })
	h.clock.advance(6 * time.Hour)
	h.eventually("the third SCIP run", waitLimit, func() bool { return count() == 3 })
	if n := strings.Count(h.log.String(), "missing indexer"); n != 1 {
		t.Fatalf("missing indexer logged %d times:\n%s", n, h.log.String())
	}
}

func TestScheduledAutodocSkipsLockedReposThenRuns(t *testing.T) {
	record := filepath.Join(t.TempDir(), "claude")
	claude := script(t, `echo "$CODEDOC_HEADLESS $*" >> '`+record+`'
echo '{"type":"result","is_error":false,"total_cost_usd":0.1,"result":"nothing"}'`)
	h := newHarness(t, "    autodoc: {every: 1h, max_items: 5}\n", "go-lib")
	o := h.options()
	o.ClaudeBin = claude
	h.start(o)
	if st := h.repoStatus("go-lib"); !st.NextAutodoc.Equal(h.clock.Now().Add(time.Hour)) {
		t.Fatalf("next autodoc %v", st.NextAutodoc)
	}
	lock, err := session.Lock(filepath.Dir(h.cfg.DBPath("fx")), "fx", "go-lib")
	if err != nil {
		t.Fatal(err)
	}
	h.clock.advance(time.Hour)
	h.eventually("a skipped run", waitLimit, func() bool {
		return strings.Contains(h.log.String(), "fx/go-lib: autodoc skipped: a session holds the repo's lock")
	})
	if _, err := os.Stat(record); !os.IsNotExist(err) {
		t.Fatalf("claude ran while the lock was held: %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	h.clock.advance(time.Hour)
	h.eventually("claude invoked", waitLimit, func() bool {
		b, _ := os.ReadFile(record)
		return strings.HasPrefix(string(b), "1 -p Document the following")
	})
	h.eventually("the run summary", waitLimit, func() bool {
		return strings.Contains(h.log.String(), "autodoc go-lib: documented 0, skipped 0, failed 1")
	})
	h.write("go-lib", "NOTES.txt", "mine\n")
	h.eventually("the note indexed", waitLimit, func() bool { return h.repoStatus("go-lib").Queue == 0 })
	h.clock.advance(time.Hour)
	h.eventually("a dirty skip", waitLimit, func() bool {
		return strings.Contains(h.log.String(), "fx/go-lib: autodoc skipped: the working tree has uncommitted changes")
	})
}

func TestFailedSCIPRunIsTheRepoLastError(t *testing.T) {
	t.Setenv("CODEDOC_SCIP_GO", script(t, "echo boom >&2; exit 1"))
	h := newHarness(t, "    scip: {enabled: [go-lib], every: 6h}\n", "go-lib")
	h.start(h.options())
	h.eventually("the scip error", waitLimit, func() bool {
		return strings.HasPrefix(h.repoStatus("go-lib").LastError, "scip: go: failed: scip: go indexer")
	})
	if r := h.repoStatus("go-lib"); r.Queue != 0 {
		t.Fatalf("%+v", r)
	}
}

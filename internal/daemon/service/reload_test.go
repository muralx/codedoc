package service

import (
	"os"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/daemon"
	"github.com/muralx/codedoc/internal/testutil"
)

func TestDaemonReloadsAChangedConfiguration(t *testing.T) {
	h := newHarness(t, "", "go-lib")
	h.start(h.options())
	h.repos["ts-domain"] = testutil.CopyFixture(t, "ts-domain")
	if err := os.WriteFile(h.cfgPath, []byte("workspaces:\n  fx:\n    repos: [{path: "+h.repos["go-lib"]+"}, {path: "+
		h.repos["ts-domain"]+"}]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.eventually("the added repo fresh", waitLimit, func() bool {
		r, err := daemon.QueryFresh(h.socket, h.query("ts-domain"))
		return err == nil && r.Fresh
	})
	h.write("ts-domain", "src/added.ts", "export function addedAfterReload(): number { return 1; }\n")
	h.eventually("an edit in the added repo", latency, func() bool {
		return strings.Contains(h.find("addedAfterReload"), "src/added.ts")
	})
	if err := os.WriteFile(h.cfgPath, []byte("workspaces: [broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.eventually("the broken file reported", waitLimit, func() bool {
		return strings.Contains(h.log.String(), "cannot be loaded, keeping the current one")
	})
	if r, err := daemon.QueryFresh(h.socket, h.query("ts-domain")); err != nil || !r.Fresh {
		t.Fatalf("a broken configuration dropped the running one: %v %+v", err, r)
	}
}

func TestMissingRepoIsReportedAndOthersStayFresh(t *testing.T) {
	h := newHarness(t, "", "go-lib", "ts-domain")
	h.start(h.options())
	away := h.repos["ts-domain"] + ".away"
	if err := os.Rename(h.repos["ts-domain"], away); err != nil {
		t.Fatal(err)
	}
	h.eventually("ts-domain missing", waitLimit, func() bool {
		st := h.repoStatus("ts-domain")
		return st.Missing && !st.Fresh && strings.Contains(st.Degraded, "root missing")
	})
	if r, err := daemon.QueryFresh(h.socket, h.query("ts-domain")); err != nil || r.Fresh || !strings.HasPrefix(r.Note, "missing: ") {
		t.Fatalf("%v %+v", err, r)
	}
	h.write("go-lib", "store/still.go", "package store\n\nfunc StillWatched() {}\n")
	h.eventually("the healthy repo indexed", latency, func() bool {
		return strings.Contains(h.find("StillWatched"), "store/still.go")
	})
	if !strings.Contains(h.find("MemoryRepo"), "records.ts") {
		t.Fatal("the missing repo's index was dropped")
	}
	if err := os.Rename(away, h.repos["ts-domain"]); err != nil {
		t.Fatal(err)
	}
	h.eventually("ts-domain back and fresh", waitLimit, func() bool {
		st := h.repoStatus("ts-domain")
		return !st.Missing && st.Fresh && st.Degraded == ""
	})
}

func TestDaemonKeepsTheConfigurationWhenTheNewOneCannotOpen(t *testing.T) {
	h := newHarness(t, "", "go-lib")
	o := h.options()
	o.Workspace = "fx"
	h.start(o)
	if err := os.WriteFile(h.cfgPath, []byte("workspaces:\n  renamed:\n    repos: [{path: "+h.repos["go-lib"]+"}]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.eventually("the failed reopening logged", waitLimit, func() bool {
		return strings.Contains(h.log.String(), "cannot open the changed configuration, keeping the previous one")
	})
	h.eventually("the previous configuration served again", waitLimit, func() bool {
		r, err := daemon.QueryFresh(h.socket, h.query("go-lib"))
		return err == nil && r.Fresh
	})
}

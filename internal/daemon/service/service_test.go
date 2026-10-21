package service

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/muralx/codedoc/internal/buildinfo"
	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/daemon"
	"github.com/muralx/codedoc/internal/embed"
)

func TestDaemonFollowsEditsDirectoriesDeletesAndBranches(t *testing.T) {
	h := newHarness(t, "", "go-lib")
	h.start(h.options())
	h.write("go-lib", "store/extra.go", "package store\n\n// Extra is new.\nfunc Extra() {}\n")
	h.eventually("an edited symbol", latency, func() bool { return strings.Contains(h.find("Extra"), "store/extra.go") })
	if n, documented := h.vectors("go-lib"); n != documented {
		t.Fatalf("vectors of the edit not embedded: %d/%d", n, documented)
	}
	h.write("go-lib", "fresh/deep/fresh.go", "package deep\n\nfunc InNewDir() {}\n")
	h.eventually("a symbol in a new directory", latency, func() bool {
		return strings.Contains(h.find("InNewDir"), "fresh/deep/fresh.go")
	})
	if err := os.RemoveAll(filepath.Join(h.repos["go-lib"], "fresh")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(h.repos["go-lib"], "service", "cache.go")); err != nil {
		t.Fatal(err)
	}
	h.eventually("deleted symbols gone", latency, func() bool {
		return !strings.Contains(h.find("InNewDir"), "fresh.go") && !strings.Contains(h.find("Cache"), "cache.go")
	})
	h.git("go-lib", "checkout", "-q", "-b", "feature")
	h.write("go-lib", "store/feature.go", "package store\n\nfunc OnFeature() {}\n")
	h.git("go-lib", "add", "-A")
	h.git("go-lib", "commit", "-qm", "feature")
	h.eventually("the feature branch", waitLimit, func() bool {
		return h.repoStatus("go-lib").Branch == "feature" && strings.Contains(h.find("OnFeature"), "feature.go")
	})
	h.git("go-lib", "checkout", "-q", "main")
	h.eventually("back on main", waitLimit, func() bool {
		st := h.repoStatus("go-lib")
		return st.Branch == "main" && st.Queue == 0 && !strings.Contains(h.find("OnFeature"), "feature.go")
	})
}

// TestDaemonEmbedsTheSymbolsOfAnEdit runs the daemon with a model that
// is not loaded, as in production, and waits on the vectors alone: a
// query embeds what is pending itself, and a loaded embedder lets the
// indexing pass embed, so either would hide a daemon that never embeds
// after the catch-up.
func TestDaemonEmbedsTheSymbolsOfAnEdit(t *testing.T) {
	h := newHarness(t, "", "go-lib")
	opts := h.options()
	opts.Embedder = embed.NewLazy(embed.FakeDims, func() (embed.Embedder, error) { return embed.NewFake(embed.FakeDims), nil }, nil).
		UnloadAfter(time.Millisecond)
	h.start(opts)
	var before int
	h.eventually("vectors embedded after the catch-up", waitLimit, func() bool {
		n, documented := h.vectors("go-lib")
		before = documented
		return documented > 0 && n == documented
	})
	h.write("go-lib", "store/embedded.go", "package store\n\n// Embedded is new and documented.\nfunc Embedded() {}\n")
	h.eventually("the edit embedded", waitLimit, func() bool {
		n, documented := h.vectors("go-lib")
		return documented > before && n == documented
	})
}

func TestDaemonAnswersStatusAndFresh(t *testing.T) {
	h := newHarness(t, "", "go-lib", "ts-domain")
	h.start(h.options())
	checkHealthy(t, h.repoStatus("go-lib"))
	if r, err := daemon.QueryFresh(h.socket, daemon.FreshQuery{Workspace: "fx", Repo: "nope"}); err != nil || r.Fresh {
		t.Fatalf("%v %+v", err, r)
	}
	checkUntrusted(t, h)
	if !strings.Contains(h.find("MemStore"), "store/store.go") {
		t.Fatal("initial index missing")
	}
	h.eventually("vectors embedded after the catch-up", waitLimit, func() bool {
		n, documented := h.vectors("go-lib")
		return documented > 0 && n == documented
	})
}

// checkUntrusted expects the daemon to answer not fresh, with a note
// saying why, to clients it does not keep current.
func checkUntrusted(t *testing.T, h *harness) {
	t.Helper()
	cases := []struct {
		name, note string
		change     func(q *daemon.FreshQuery)
	}{
		{"another database", "serves " + h.repos["go-lib"], func(q *daemon.FreshQuery) { q.DB = filepath.Join(t.TempDir(), "other.db") }},
		{"a recreated database", "daemon has a different database open", func(q *daemon.FreshQuery) { q.DBID.Ino++ }},
		{"another build", "daemon is running another codedoc build (" + buildinfo.Version + ", extractor",
			func(q *daemon.FreshQuery) { q.Version = "v0.0.1-old" }},
		{"another extractor version", "restart it", func(q *daemon.FreshQuery) { q.Extractor = "0" }},
	}
	for _, c := range cases {
		q := h.query("go-lib")
		c.change(&q)
		if r, err := daemon.QueryFresh(h.socket, q); err != nil || r.Fresh || !strings.Contains(r.Note, c.note) {
			t.Errorf("%s trusted the daemon: %v %+v", c.name, err, r)
		}
	}
}

// checkHealthy expects the status of a caught-up, healthy repo.
func checkHealthy(t *testing.T, st daemon.RepoStatus) {
	t.Helper()
	if st.Branch != "main" || len(st.Commit) != 40 || st.IndexedAt.IsZero() || st.Queue != 0 || st.LastError != "" ||
		!st.NextSCIP.IsZero() || !st.NextAutodoc.IsZero() || !st.Fresh || st.Degraded != "" || st.Missing {
		t.Fatalf("%+v", st)
	}
}

func TestDaemonShutsDownGracefully(t *testing.T) {
	h := newHarness(t, "", "go-lib")
	h.start(h.options())
	if err := h.stop(); err != nil {
		t.Fatal(err)
	}
	if _, err := daemon.QueryStatus(h.socket); !errors.Is(err, daemon.ErrNoDaemon) {
		t.Fatalf("socket still answers: %v", err)
	}
	if !strings.Contains(h.log.String(), "daemon: stopped") {
		t.Fatal(h.log.String())
	}
}

func TestSecondDaemonFailsAtOnce(t *testing.T) {
	h := newHarness(t, "", "go-lib")
	h.start(h.options())
	start := time.Now()
	if _, err := New(h.options()); !errors.Is(err, daemon.ErrRunning) {
		t.Fatalf("second daemon on the data directory: %v", err)
	}
	o := h.options()
	t.Setenv("CODEDOC_DATA", filepath.Join(t.TempDir(), "other"))
	cfg, err := config.Load(h.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	o.Config = cfg
	if _, err := New(o); !errors.Is(err, daemon.ErrSocketInUse) {
		t.Fatalf("second daemon on the socket: %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("refusing took %s", d)
	}
}

func TestNewRejectsBadSelections(t *testing.T) {
	h := newHarness(t, "", "go-lib")
	o := h.options()
	o.Workspace = "nope"
	if _, err := New(o); err == nil {
		t.Fatal("unknown workspace accepted")
	}
	o.Workspace = ""
	delete(o.Config.Workspaces, "fx")
	if _, err := New(o); err == nil {
		t.Fatal("empty config accepted")
	}
}

func TestSafelyRecordsAndClearsErrors(t *testing.T) {
	h := newHarness(t, "", "go-lib")
	s, err := New(h.options())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	g, err := s.open(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer g.close()
	r := g.repo("fx", "go-lib")
	if err := r.safely("index", func() error { panic("boom") }); err == nil || !strings.Contains(r.status().LastError, "index: panic: boom") {
		t.Fatalf("%v %+v", err, r.status())
	}
	_ = r.safely("scip", func() error { return nil })
	if r.status().LastError == "" {
		t.Fatal("another kind cleared the error")
	}
	_ = r.safely("index", func() error { return nil })
	if r.status().LastError != "" {
		t.Fatal("success did not clear the error")
	}
	if g.repo("fx", "nope") != nil || g.repo("other", "go-lib") != nil {
		t.Fatal("unknown repo found")
	}
	if s.Status().Workspaces != nil || s.Fresh(h.query("go-lib")).Note != "daemon: starting" {
		t.Fatal("a service without a generation answered")
	}
	if got := trimNewline("a\r\n\n"); got != "a" {
		t.Fatal(got)
	}
}

func TestDefaultsAndLogWriter(t *testing.T) {
	o := withDefaults(Options{})
	if o.SocketPath != daemon.SocketPath() || o.Now == nil || o.Tick != 30*time.Second || o.AutodocMinutes != 30 ||
		o.ConfigPoll != 2*time.Second {
		t.Fatalf("%+v", o)
	}
	buf := &lockedBuffer{}
	log, err := daemon.NewLogger(buf, "")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := (logWriter{log}).Write([]byte("codedoc: model missing\n")); err != nil || n != 23 ||
		!strings.HasSuffix(buf.String(), " codedoc: model missing\n") {
		t.Fatalf("%d %v %q", n, err, buf.String())
	}
}

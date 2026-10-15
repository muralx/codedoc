package upkeep_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/muralx/codedoc/internal/autodoc"
	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/store"
	"github.com/muralx/codedoc/internal/testutil"
	"github.com/muralx/codedoc/internal/upkeep"
)

// libKey is the stable key prefix of package lib of the fixture repo.
const libKey = "lib:go:example.com/lib:"

// fixture is an indexed, committed Go repo "lib" on branch main whose
// one package is fully documented, with upkeep enabled at its defaults,
// a settable clock, and the test binary as stub claude.
type fixture struct {
	t     *testing.T
	root  string
	store *store.Store
	ws    *config.Workspace
	now   time.Time
	log   bytes.Buffer
	skip  map[string]bool
	// record is the file the stub appends its command lines to.
	record string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv(stubEnv, "ok")
	f := &fixture{t: t, now: time.Date(2026, 10, 9, 12, 0, 0, 0, time.Local), skip: map[string]bool{},
		record: filepath.Join(t.TempDir(), "claude.log")}
	t.Setenv(stubRecordEnv, f.record)
	f.root = testutil.SourceRepo(t, "lib", map[string]string{
		"go.mod": "module example.com/lib\n\ngo 1.22\n",
		"lib.go": "// Package lib is the fixture.\npackage lib\n\n// Old is documented.\nfunc Old() int {\n\treturn 1\n}\n",
	})
	f.git("config", "user.name", "Repo Owner")
	f.git("config", "user.email", "owner@example.com")
	f.git("config", "commit.gpgsign", "false")
	repo := &config.Repo{Name: "lib", Path: f.root}
	f.ws = &config.Workspace{Name: "fx", Repos: []*config.Repo{repo}, Upkeep: config.Upkeep{Enabled: true,
		Quiet: config.Duration(5 * time.Minute), MaxItems: 20, FirstRunCommits: 20, MaxCostUSD: 1, DailyCostUSD: 5}}
	s, err := store.Open(filepath.Join(t.TempDir(), "fx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	f.store = s
	opts := index.Options{Workspace: "fx", RepoOrder: []string{"lib"}}
	if _, err := index.Repo(s, repo, opts); err != nil {
		t.Fatal(err)
	}
	if err := index.ResolvePending(s, opts); err != nil {
		t.Fatal(err)
	}
	return f
}

// Printf makes fixture a Logger.
func (f *fixture) Printf(format string, args ...any) { fmt.Fprintf(&f.log, format+"\n", args...) }

// env is the upkeep environment of the fixture; the last file event was
// an hour before the fixture's clock.
func (f *fixture) env() upkeep.Env {
	exe, err := os.Executable()
	if err != nil {
		f.t.Fatal(err)
	}
	return upkeep.Env{Store: f.store, Workspace: f.ws, Repo: f.ws.Repos[0], Now: func() time.Time { return f.now },
		LastEvent: f.now.Add(-time.Hour), Skip: f.skip, Log: f, ClaudeBin: exe}
}

// check evaluates the fixture.
func (f *fixture) check() upkeep.Check {
	f.t.Helper()
	c, err := upkeep.Evaluate(f.env())
	if err != nil {
		f.t.Fatal(err)
	}
	return c
}

// run evaluates the fixture, which must be ready, and runs upkeep.
func (f *fixture) run() upkeep.Result {
	f.t.Helper()
	c := f.check()
	if !c.Ready() {
		f.t.Fatalf("not ready: %+v", c)
	}
	res, err := upkeep.Run(context.Background(), f.env(), c)
	if err != nil {
		f.t.Fatalf("%v\nlog:\n%s", err, f.log.String())
	}
	return res
}

// git runs git in the repo and returns its trimmed output.
func (f *fixture) git(args ...string) string {
	f.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = f.root
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// write writes a file of the repo.
func (f *fixture) write(rel, body string) {
	f.t.Helper()
	p := filepath.Join(f.root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// read returns a file of the repo.
func (f *fixture) read(rel string) string {
	f.t.Helper()
	b, err := os.ReadFile(filepath.Join(f.root, rel))
	if err != nil {
		f.t.Fatal(err)
	}
	return string(b)
}

// add commits a new file <name>.go of package lib holding the
// undocumented exported function <Name>, and returns the commit.
func (f *fixture) add(name string) string {
	f.t.Helper()
	f.write(strings.ToLower(name)+".go", "package lib\n\nfunc "+name+"() int {\n\treturn 1\n}\n")
	f.git("add", "-A")
	f.git("commit", "-q", "-m", "add "+name)
	return f.git("rev-parse", "HEAD")
}

// state returns the last documented commit of a branch, "" without one.
func (f *fixture) state(branch string) string {
	f.t.Helper()
	row, err := f.store.RepoByPath(f.root)
	if err != nil {
		f.t.Fatal(err)
	}
	u, _, err := f.store.UpkeepBranch(row.ID, branch)
	if err != nil {
		f.t.Fatal(err)
	}
	return u.Commit
}

// keys joins the keys of gaps without the package prefix.
func keys(gaps []autodoc.Gap) string {
	var out []string
	for _, g := range gaps {
		out = append(out, strings.TrimPrefix(g.Key, libKey))
	}
	return strings.Join(out, " ")
}

// same fails the test, with the run's log, when got differs from want.
func same[T comparable](f *fixture, what string, got, want T) {
	f.t.Helper()
	if got != want {
		f.t.Fatalf("%s: got %v, want %v\nlog:\n%s", what, got, want, f.log.String())
	}
}

package autodoc

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	srcA = "package a\n\nfunc A1() {}\n\nfunc A2() {}\n"
	srcB = "package b\n\nfunc B1() {}\n"
)

// testGaps ranks A1, then B1, then A2: module a's group comes first.
var testGaps = []Gap{
	{Key: "r:go:a:A1", Module: "a", Path: "a/a.go", Line: 3, State: "missing", Score: 3},
	{Key: "r:go:b:B1", Module: "b", Path: "b/b.go", Line: 3, State: "missing", Score: 2},
	{Key: "r:go:a:A2", Module: "a", Path: "a/a.go", Line: 5, State: "missing", Score: 1},
}

// fakeGaps serves a fixed list.
type fakeGaps struct {
	list []Gap
	err  error
}

func (f fakeGaps) Next(_ string, limit int) ([]Gap, error) {
	if limit > 0 && limit < len(f.list) {
		return f.list[:limit], f.err
	}
	return f.list, f.err
}

// fakeIndex judges doc states from the files on disk: a function is ok
// when it carries the stub's good doc, stale when it carries a broken
// one, and missing otherwise. Each Reindex call advances clock by step.
type fakeIndex struct {
	root     string
	mu       sync.Mutex
	reindex  [][]string
	clock    *fakeClock
	step     time.Duration
	failNext bool
}

func (f *fakeIndex) Reindex(_ string, paths []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reindex = append(f.reindex, paths)
	if f.clock != nil {
		f.clock.advance(f.step)
	}
	if f.failNext {
		f.failNext = false
		return errors.New("index broke")
	}
	return nil
}

func (f *fakeIndex) States(keys []string) (map[string]string, error) {
	out := map[string]string{}
	for _, k := range keys {
		p := strings.SplitN(k, ":", 4)
		src, err := os.ReadFile(filepath.Join(f.root, p[2], p[2]+".go"))
		if err != nil {
			return nil, err
		}
		s := string(src)
		switch {
		case strings.Contains(s, "// "+p[3]+" does things.\nfunc "+p[3]+"("):
			out[k] = StateOK
		case strings.Contains(s, "// "+p[3]+" is BROKEN.\nfunc "+p[3]+"("):
			out[k] = "stale"
		default:
			out[k] = "missing"
		}
	}
	return out, nil
}

// Docs reads the doc comment line above each func of a/a.go and b/b.go.
func (f *fakeIndex) Docs(string) (map[string]DocAt, error) {
	out := map[string]DocAt{}
	for _, dir := range []string{"a", "b"} {
		rel := dir + "/" + dir + ".go"
		src, err := os.ReadFile(filepath.Join(f.root, rel))
		if err != nil {
			return nil, err
		}
		lines := strings.Split(string(src), "\n")
		for i, l := range lines {
			name, ok := strings.CutPrefix(l, "func ")
			if !ok {
				continue
			}
			name, _, _ = strings.Cut(name, "(")
			doc := ""
			if i > 0 && strings.HasPrefix(lines[i-1], "// ") {
				doc = lines[i-1]
			}
			out["r:go:"+dir+":"+name] = DocAt{Path: rel, Hash: doc}
		}
	}
	return out, nil
}

// fakeClock is a settable clock.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// fixture is a committed repo with modules a and b, a private journal
// state directory, and the test binary as stub claude in mode.
type fixture struct {
	root   string
	record string
	idx    *fakeIndex
	log    *bytes.Buffer
}

func newFixture(t *testing.T, mode string) *fixture {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv(stubEnv, mode)
	rec := filepath.Join(t.TempDir(), "record.jsonl")
	t.Setenv(stubRecordEnv, rec)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	git(t, root, "init", "-q", "-b", "main")
	git(t, root, "config", "user.name", "Repo Owner")
	git(t, root, "config", "user.email", "owner@example.com")
	git(t, root, "config", "commit.gpgsign", "false")
	writeFile(t, root, "a/a.go", srcA)
	writeFile(t, root, "b/b.go", srcB)
	writeFile(t, root, "go.mod", "module x\n\ngo 1.22\n")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "init")
	return &fixture{root: root, record: rec, idx: &fakeIndex{root: root}, log: &bytes.Buffer{}}
}

// options returns run options for the fixture using the test binary as
// claude.
func (f *fixture) options() Options {
	exe, err := os.Executable()
	if err != nil {
		panic(err)
	}
	return Options{Repo: "r", Root: f.root, ClaudeBin: exe}
}

// Printf makes fixture a Logger.
func (f *fixture) Printf(format string, args ...any) {
	f.log.WriteString(strings.TrimSpace(fmt.Sprintf(format, args...)) + "\n")
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

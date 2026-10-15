package docs_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/docs"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/store"
)

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.email=t@x", "-c", "user.name=t"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE=2026-10-01T00:00:00Z", "GIT_COMMITTER_DATE=2026-10-01T00:00:00Z")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestRepoGaps(t *testing.T) {
	root := t.TempDir()
	write(t, root, "go.mod", "module x/m\n")
	write(t, root, "lib/lib.go", "package lib\n\n// Iface is documented.\ntype Iface interface {\n\tDo()\n}\n\n"+
		"type T struct{ name string }\n\nfunc (t *T) Name() string {\n\treturn t.name\n}\n\nfunc Long() int {\n\tx := 1\n\treturn x\n}\n")
	write(t, root, "lib/lib_test.go", "package lib\n\nfunc helper() int {\n\tx := 1\n\treturn x\n}\n")
	gitRun(t, root, "init", "-q", "-b", "main")
	gitRun(t, root, "add", "-A")
	gitRun(t, root, "commit", "-qm", "init")
	write(t, root, "lib/new.go", "package lib\n\nfunc Fresh() int {\n\tx := 2\n\treturn x\n}\n")
	s, err := store.Open(filepath.Join(t.TempDir(), "w.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	rep := &config.Repo{Name: "r", Path: root}
	if _, err := index.Repo(s, rep, index.Options{Workspace: "w"}); err != nil {
		t.Fatal(err)
	}
	row, err := s.RepoByPath(root)
	if err != nil {
		t.Fatal(err)
	}
	gaps, err := docs.RepoGaps(s, docs.RepoRef{ID: row.ID, Name: "r", Root: root}, time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, g := range gaps {
		got = append(got, g.Key+" "+g.Reason)
	}
	want := []string{
		"r:lib/ module, fan-in 0, uncommitted change",
		"r:go:x/m/lib:T public, fan-in 0, changed 8d ago",
		"r:go:x/m/lib:Fresh public, fan-in 0, uncommitted change",
		"r:go:x/m/lib:Long public, fan-in 0, changed 8d ago",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestHeadings(t *testing.T) {
	got := docs.Headings("# A\ntext\n## B\n### C\n#nospace\n```\n# code\n```\n## D\n", 2)
	if strings.Join(got, "|") != "# A|## B|## D" {
		t.Fatalf("%q", got)
	}
}

func TestRepoGapsDropSyntheticSymbols(t *testing.T) {
	root := t.TempDir()
	write(t, root, "src/main/java/p/Point.java", "package p;\n\n/** A point. */\npublic record Point(int x, int y) {\n}\n")
	write(t, root, "package.json", "{\"name\": \"w\"}\n")
	write(t, root, "src/a.ts", "/** Adds. */\nexport function add(a: number, b: number): number {\n  const c = a + b;\n  return c;\n}\n")
	s, err := store.Open(filepath.Join(t.TempDir(), "w.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	rep := &config.Repo{Name: "r", Path: root}
	if _, err := index.Repo(s, rep, index.Options{Workspace: "w"}); err != nil {
		t.Fatal(err)
	}
	row, err := s.RepoByPath(root)
	if err != nil {
		t.Fatal(err)
	}
	gaps, err := docs.RepoGaps(s, docs.RepoRef{ID: row.ID, Name: "r", Root: root}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range gaps {
		if g.Kind == "method" && strings.Contains(g.Key, "Point.") {
			t.Fatalf("record accessor listed: %+v", g)
		}
	}
}

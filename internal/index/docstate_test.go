package index_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/store"
)

const libStore = "go-lib:go:github.com/fixture/lib/store:"

func docState(t *testing.T, s *store.Store, key string) string {
	t.Helper()
	sy, err := s.SymbolByKey(key)
	if err != nil {
		t.Fatalf("%s: %v", key, err)
	}
	return sy.DocState
}

func replaceIn(t *testing.T, root, rel, old, repl string) {
	t.Helper()
	p := filepath.Join(root, rel)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), old) {
		t.Fatalf("%s has no %q", rel, old)
	}
	writeFile(t, root, rel, []byte(strings.Replace(string(b), old, repl, 1)))
}

// gitCommitAt commits every change in dir with a fixed date.
func gitCommitAt(t *testing.T, dir, msg, date string) {
	t.Helper()
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", msg}} {
		cmd := exec.Command("git", append([]string{"-c", "user.email=t@x", "-c", "user.name=t"}, args...)...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func TestDocStateFollowsBodyAndDocEdits(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	indexRepo(t, s, rep)
	if got := docState(t, s, libStore+"MemStore.Get"); got != "ok" {
		t.Fatalf("fresh index: %s", got)
	}
	if got := docState(t, s, libStore+"MemStore.data"); got != "missing" {
		t.Fatalf("undocumented field: %s", got)
	}
	replaceIn(t, rep.Path, "store/store.go", "return v, nil", "return v + \"\", nil")
	ensure(t, s, rep, 200)
	if got := docState(t, s, libStore+"MemStore.Get"); got != "stale" {
		t.Fatalf("body changed: %s", got)
	}
	if got := docState(t, s, libStore+"MemStore.Put"); got != "ok" {
		t.Fatalf("untouched sibling: %s", got)
	}
	replaceIn(t, rep.Path, "store/store.go", "\t\treturn \"\", ErrMissing", "\t\treturn \"\",   ErrMissing")
	ensure(t, s, rep, 200)
	if got := docState(t, s, libStore+"MemStore.Get"); got != "stale" {
		t.Fatalf("whitespace edit: %s", got)
	}
	replaceIn(t, rep.Path, "store/store.go", "// Get implements Store.", "// Get implements Store; a missing key is ErrMissing.")
	ensure(t, s, rep, 200)
	if got := docState(t, s, libStore+"MemStore.Get"); got != "ok" {
		t.Fatalf("doc changed: %s", got)
	}
	replaceIn(t, rep.Path, "store/store.go", "Put(key, value string) error {", "Put(k, value string) error {\n\tkey := k")
	ensure(t, s, rep, 200)
	if got := docState(t, s, libStore+"MemStore.Put"); got != "stale" {
		t.Fatalf("signature changed: %s", got)
	}
}

func TestDocStateSeedsNewSymbolsFromBlame(t *testing.T) {
	rep := repoFixture(t, "go-lib")
	writeFile(t, rep.Path, "store/seed.go", []byte("package store\n\n// Old is documented.\nfunc Old() int {\n\treturn 1\n}\n\n"+
		"// Same is documented.\nfunc Same() int {\n\treturn 1\n}\n"))
	gitCommitAt(t, rep.Path, "seed", "2026-02-01T00:00:00Z")
	replaceIn(t, rep.Path, "store/seed.go", "func Old() int {\n\treturn 1", "func Old() int {\n\treturn 2")
	gitCommitAt(t, rep.Path, "body", "2026-03-01T00:00:00Z")
	s := openTemp(t)
	indexRepo(t, s, rep)
	if got := docState(t, s, libStore+"Old"); got != "stale" {
		t.Fatalf("body newer than doc in blame: %s", got)
	}
	if got := docState(t, s, libStore+"Same"); got != "ok" {
		t.Fatalf("doc as new as body: %s", got)
	}
	appendFile(t, rep.Path, "store/seed.go", "\n// Added is documented.\nfunc Added() int {\n\treturn 1\n}\n")
	gitCommitAt(t, rep.Path, "added", "2026-04-01T00:00:00Z")
	replaceIn(t, rep.Path, "store/seed.go", "func Added() int {\n\treturn 1", "func Added() int {\n\treturn 3")
	ensure(t, s, rep, 200)
	if got := docState(t, s, libStore+"Added"); got != "stale" {
		t.Fatalf("new symbol with an uncommitted body edit: %s", got)
	}
	if got := docState(t, s, libStore+"Old"); got != "stale" {
		t.Fatalf("seeded stale doc must stay stale until the doc changes: %s", got)
	}
	writeFile(t, rep.Path, "store/untracked.go", []byte("package store\n\n// Fresh is documented.\nfunc Fresh() {}\n"))
	ensure(t, s, rep, 200)
	if got := docState(t, s, libStore+"Fresh"); got != "ok" {
		t.Fatalf("untracked file: %s", got)
	}
}

func TestDocStateWithoutGit(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", []byte("module x/m\n"))
	writeFile(t, root, "a.go", []byte("package m\n\n// F is documented.\nfunc F() {}\n\nfunc G() {}\n"))
	s := openTemp(t)
	rep := &config.Repo{Path: root, Name: "m"}
	indexRepo(t, s, rep)
	if got := docState(t, s, "m:go:x/m:F"); got != "ok" {
		t.Fatalf("documented symbol outside git: %s", got)
	}
	if got := docState(t, s, "m:go:x/m:G"); got != "missing" {
		t.Fatalf("undocumented symbol: %s", got)
	}
}

func TestFilesReindexesNamedPathsOnly(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	indexRepo(t, s, rep)
	appendFile(t, rep.Path, "store/null.go", "\n// Named is new.\nfunc Named() {}\n")
	appendFile(t, rep.Path, "store/store.go", "\nfunc Other() {}\n")
	if err := os.Remove(filepath.Join(rep.Path, "service", "gen.go")); err != nil {
		t.Fatal(err)
	}
	opts := index.Options{Workspace: "fixture"}
	if err := index.Files(s, rep, opts, []string{"store/null.go", "service/gen.go", "nowhere.go"}); err != nil {
		t.Fatal(err)
	}
	if got := docState(t, s, libStore+"Named"); got != "ok" {
		t.Fatalf("named path: %s", got)
	}
	if _, err := s.SymbolByKey(libStore + "Other"); err == nil {
		t.Fatal("a path not named was re-extracted")
	}
	r, _ := s.RepoByPath(rep.Path)
	if _, err := s.FileByPath(r.ID, "service/gen.go"); err == nil {
		t.Fatal("a gone path keeps its row")
	}
}

func TestDocStateSeesWhitespaceInsideLiterals(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	writeFile(t, rep.Path, "store/greet.go", []byte("package store\n\n// Greet greets.\nfunc Greet() string {\n\treturn \"a b\"\n}\n"))
	gitCommitAt(t, rep.Path, "greet", "2026-02-01T00:00:00Z")
	indexRepo(t, s, rep)
	if got := docState(t, s, libStore+"Greet"); got != "ok" {
		t.Fatalf("fresh index: %s", got)
	}
	replaceIn(t, rep.Path, "store/greet.go", "{\n\treturn \"a b\"\n}", "{\n\n  return    \"a b\" }")
	ensure(t, s, rep, 200)
	if got := docState(t, s, libStore+"Greet"); got != "ok" {
		t.Fatalf("reformatted code: %s", got)
	}
	replaceIn(t, rep.Path, "store/greet.go", "\"a b\"", "\"ab\"")
	ensure(t, s, rep, 200)
	if got := docState(t, s, libStore+"Greet"); got != "stale" {
		t.Fatalf("a literal lost a space: %s", got)
	}
}

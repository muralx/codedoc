package index_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/index"
	"github.com/muralx/codedoc/internal/store"
)

// gitRun runs git in dir and fails the test on error.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.email=t@x", "-c", "user.name=t"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func wantState(t *testing.T, got, want, what string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: doc state %s, want %s", what, got, want)
	}
}

func TestContainerDocIgnoresMemberDocsAndBodies(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	indexRepo(t, s, rep)
	wantState(t, docState(t, s, libStore+"MemStore"), "ok", "fresh index")
	replaceIn(t, rep.Path, "store/store.go", "\tdata map[string]string\n", "\t// data holds the values.\n\tdata map[string]string\n")
	ensure(t, s, rep, 200)
	wantState(t, docState(t, s, libStore+"MemStore"), "ok", "member doc added")
	replaceIn(t, rep.Path, "store/store.go", "\t// Put stores value under key.\n", "\t// Put stores value under key, replacing any.\n")
	ensure(t, s, rep, 200)
	wantState(t, docState(t, s, libStore+"Store"), "ok", "interface member doc edited")
	replaceIn(t, rep.Path, "store/store.go", "\tdata map[string]string\n", "\tdata map[string]string\n\tn    int\n")
	ensure(t, s, rep, 200)
	wantState(t, docState(t, s, libStore+"MemStore"), "stale", "field added")
	replaceIn(t, rep.Path, "store/store.go", "\tPut(key, value string) error\n", "\tPut(key, value string) error\n\tLen() int\n")
	ensure(t, s, rep, 200)
	wantState(t, docState(t, s, libStore+"Store"), "stale", "method added")
}

func TestContainerSeedIgnoresMemberBodies(t *testing.T) {
	rep := repoFixture(t, "go-lib")
	writeFile(t, rep.Path, "store/box.py", []byte("class Box:\n    \"\"\"Box holds one value.\"\"\"\n\n"+
		"    def get(self):\n        return 1\n"))
	gitCommitAt(t, rep.Path, "box", "2026-02-01T00:00:00Z")
	replaceIn(t, rep.Path, "store/box.py", "return 1", "return 2")
	gitCommitAt(t, rep.Path, "body", "2026-03-01T00:00:00Z")
	s := openTemp(t)
	indexRepo(t, s, rep)
	sy := symbolNamed(t, s, rep, "Box")
	wantState(t, sy.DocState, "ok", "class whose method body is newer than its doc")
}

func TestMovedSymbolKeepsDocState(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	indexRepo(t, s, rep)
	replaceIn(t, rep.Path, "store/store.go", "return v, nil", "return v + \"\", nil")
	ensure(t, s, rep, 200)
	wantState(t, docState(t, s, libStore+"MemStore.Get"), "stale", "body changed")
	src := readFile(t, rep.Path, "store/store.go")
	start := strings.Index(src, "// Get implements Store.")
	end := strings.Index(src, "// Put implements Store.")
	writeFile(t, rep.Path, "store/store.go", []byte(src[:start]+src[end:]))
	writeFile(t, rep.Path, "store/get.go", []byte("package store\n\n"+src[start:end]))
	ensure(t, s, rep, 200)
	wantState(t, docState(t, s, libStore+"MemStore.Get"), "stale", "moved to another file")
}

func TestDocRevertedByGitIsStaleAgain(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	indexRepo(t, s, rep)
	replaceIn(t, rep.Path, "store/store.go", "return v, nil", "return v + \"\", nil")
	gitCommitAt(t, rep.Path, "get: copy", "2026-02-01T00:00:00Z")
	ensure(t, s, rep, 200)
	wantState(t, docState(t, s, libStore+"MemStore.Get"), "stale", "body committed after the doc")
	replaceIn(t, rep.Path, "store/store.go", "// Get implements Store.", "// Get returns a copy of the value.")
	ensure(t, s, rep, 200)
	wantState(t, docState(t, s, libStore+"MemStore.Get"), "ok", "doc edited")
	gitRun(t, rep.Path, "checkout", "--", "store/store.go")
	ensure(t, s, rep, 200)
	wantState(t, docState(t, s, libStore+"MemStore.Get"), "stale", "doc checked out again")
}

func TestBranchSwitchRestoringOlderDoc(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	replaceIn(t, rep.Path, "store/store.go", "return v, nil", "return v + \"\", nil")
	gitCommitAt(t, rep.Path, "get: copy", "2026-02-01T00:00:00Z")
	gitRun(t, rep.Path, "branch", "old")
	replaceIn(t, rep.Path, "store/store.go", "// Get implements Store.", "// Get returns a copy of the value.")
	gitCommitAt(t, rep.Path, "get: doc", "2026-03-01T00:00:00Z")
	indexRepo(t, s, rep)
	wantState(t, docState(t, s, libStore+"MemStore.Get"), "ok", "doc committed after the body")
	gitRun(t, rep.Path, "checkout", "-q", "old")
	ensure(t, s, rep, 200)
	wantState(t, docState(t, s, libStore+"MemStore.Get"), "stale", "branch with the older doc")
}

func TestGroupDocCoversMembers(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	writeFile(t, rep.Path, "store/kinds.go", []byte("package store\n\n// Kinds of values.\nconst (\n\tKindA = 1\n\tKindB = 2\n)\n\n"+
		"const (\n\tLoneA = 1\n)\n"))
	indexRepo(t, s, rep)
	wantState(t, docState(t, s, libStore+"KindA"), "ok", "member of a documented group")
	wantState(t, docState(t, s, libStore+"LoneA"), "missing", "member of an undocumented group")
}

func TestExtractorVersionChangeReextractsEverything(t *testing.T) {
	s := openTemp(t)
	rep := repoFixture(t, "go-lib")
	indexRepo(t, s, rep)
	res, err := index.Repo(s, rep, index.Options{Workspace: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if res.FilesIndexed != 0 {
		t.Fatalf("unchanged repo re-extracted %d files", res.FilesIndexed)
	}
	index.SetStoredExtractorVersion(t, s, rep.Path, "0")
	f := ensure(t, s, rep, 200)
	if f.Changed == 0 {
		t.Fatal("an older extractor version must re-extract every file")
	}
	if f = ensure(t, s, rep, 200); f.Changed != 0 {
		t.Fatalf("the version is recorded after a full sync: %d changed", f.Changed)
	}
	wantState(t, docState(t, s, libStore+"MemStore.Get"), "ok", "reseeded from blame")
}

func readFile(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(root + "/" + rel)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// symbolNamed returns the one symbol of a repo with the name.
func symbolNamed(t *testing.T, s *store.Store, rep *config.Repo, name string) store.Symbol {
	t.Helper()
	r, err := s.RepoByPath(rep.Path)
	if err != nil {
		t.Fatal(err)
	}
	syms, err := s.SymbolsByName(name, "", r.ID)
	if err != nil || len(syms) != 1 {
		t.Fatalf("%s: %v %v", name, syms, err)
	}
	return syms[0]
}

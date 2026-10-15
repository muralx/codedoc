package write

import (
	"os"
	"path/filepath"
	"testing"
)

// repoNamed creates a repo directory named name under a fresh parent.
func repoNamed(t *testing.T, name string, files map[string]string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	for p, body := range files {
		abs := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestJournalKeepsReposWithTheSameName(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv(RunIDEnv, "run-2")
	one := repoNamed(t, "api", map[string]string{"a.go": "package a\n\nfunc F() {}\n"})
	two := repoNamed(t, "api", map[string]string{"a.go": "package a\n\n// F was documented.\nfunc F() {}\n"})
	origOne, origTwo := read(t, one, "a.go"), read(t, two, "a.go")
	for _, root := range []string{one, two} {
		if _, err := Apply(Target{Root: root, Path: "a.go", Lang: "go", Qualified: "F"}, "F does a lot."); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Restore("run-2", false); err != nil {
		t.Fatal(err)
	}
	if read(t, one, "a.go") != origOne || read(t, two, "a.go") != origTwo {
		t.Fatal("repos with the same base name restored each other's files")
	}
}

func TestJournalIgnoresCopiesTheManifestDoesNotList(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv(RunIDEnv, "run-3")
	root := repoNamed(t, "api", map[string]string{"a.go": "package a\n\nfunc F() {}\n"})
	orig := read(t, root, "a.go")
	stale := filepath.Join(state, "codedoc", "runs", "run-3", journalDir(root), "a.go")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("left by a crash\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(Target{Root: root, Path: "a.go", Lang: "go", Qualified: "F"}, "F does a lot."); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore("run-3", false); err != nil {
		t.Fatal(err)
	}
	if read(t, root, "a.go") != orig {
		t.Fatalf("restored %q", read(t, root, "a.go"))
	}
}

package write

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunDir(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/state")
	if d, err := RunDir("r1"); err != nil || d != "/state/codedoc/runs/r1" {
		t.Fatalf("%s %v", d, err)
	}
	t.Setenv("XDG_STATE_HOME", "relative")
	t.Setenv("HOME", "/home/u")
	if d, err := RunDir("r1"); err != nil || d != "/home/u/.local/state/codedoc/runs/r1" {
		t.Fatalf("%s %v", d, err)
	}
	for _, id := range []string{"", ".", "..", "a/b", `a\b`} {
		if _, err := RunDir(id); err == nil {
			t.Errorf("%q accepted", id)
		}
	}
}

func TestJournalAndRestore(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv(RunIDEnv, "run-1")
	root := repo(t, map[string]string{"p/a.go": "package p\n\nfunc F() {}\n", "p/b.go": "package p\n"})
	orig := read(t, root, "p/a.go")
	tg := Target{Root: root, Path: "p/a.go", Lang: "go", Qualified: "F"}
	for _, text := range []string{"F one.", "F two."} {
		if _, err := Apply(tg, text); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Apply(Target{Root: root, Path: "p", Lang: "go", ModuleDir: "p"}, "Does p."); err != nil {
		t.Fatal(err)
	}
	checkJournal(t, state, root, orig)
	if _, err := Restore("run-1", false); err != nil {
		t.Fatal(err)
	}
	checkRestored(t, state, root, orig)
}

func TestRestoreUnlessLeavesSkippedFilesAlone(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv(RunIDEnv, "run-skip")
	root := repo(t, map[string]string{"p/a.go": "package p\n\nfunc F() {}\n", "p/b.go": "package p\n\nfunc G() {}\n"})
	origA, origB := read(t, root, "p/a.go"), read(t, root, "p/b.go")
	for file, name := range map[string]string{"p/a.go": "F", "p/b.go": "G"} {
		if _, err := Apply(Target{Root: root, Path: file, Lang: "go", Qualified: name}, name+" works."); err != nil {
			t.Fatal(err)
		}
	}
	kept, err := RestoreUnless("run-skip", false, func(abs string) bool { return abs == filepath.Join(root, "p", "b.go") })
	if err != nil || len(kept) != 0 {
		t.Fatalf("%v %v", kept, err)
	}
	if read(t, root, "p/a.go") != origA || read(t, root, "p/b.go") == origB {
		t.Fatalf("a.go must be restored and b.go left as written:\n%s\n%s", read(t, root, "p/a.go"), read(t, root, "p/b.go"))
	}
}

func TestJournalErrors(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	if _, err := Restore("../x", false); err == nil {
		t.Fatal("bad id accepted")
	}
	dir := filepath.Join(state, "codedoc", "runs", "bad")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, manifestName), []byte("{nope\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Files("bad"); err == nil {
		t.Fatal("corrupt manifest accepted")
	}
	if _, err := Restore("bad", false); err == nil {
		t.Fatal("corrupt manifest restored")
	}
	t.Setenv(RunIDEnv, "a/b")
	root := repo(t, map[string]string{"a.go": "package a\n\nfunc F() {}\n"})
	if _, err := Apply(Target{Root: root, Path: "a.go", Lang: "go", Qualified: "F"}, "F."); err == nil {
		t.Fatal("bad run id accepted")
	}
}

// checkJournal expects the original a.go saved and both files listed.
func checkJournal(t *testing.T, state, root, orig string) {
	t.Helper()
	saved := filepath.Join(state, "codedoc", "runs", "run-1", journalDir(root), "p", "a.go")
	if b, err := os.ReadFile(saved); err != nil || string(b) != orig {
		t.Fatalf("journal copy %q %v", b, err)
	}
	files, err := Files("run-1")
	want := []string{filepath.Join(root, "p", "a.go"), filepath.Join(root, "p", "doc.go")}
	if err != nil || len(files) != 2 || files[0] != want[0] || files[1] != want[1] {
		t.Fatalf("%v %v", files, err)
	}
}

// checkRestored expects a.go back, the created doc.go and the journal gone.
func checkRestored(t *testing.T, state, root, orig string) {
	t.Helper()
	if read(t, root, "p/a.go") != orig {
		t.Fatal("a.go not restored")
	}
	if _, err := os.Stat(filepath.Join(root, "p", "doc.go")); !os.IsNotExist(err) {
		t.Fatalf("created doc.go not removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(state, "codedoc", "runs", "run-1")); !os.IsNotExist(err) {
		t.Fatal("journal not removed")
	}
	if files, err := Files("run-1"); err != nil || len(files) != 0 {
		t.Fatalf("after restore: %v %v", files, err)
	}
}

func TestRestoreKeepsFilesChangedAfterTheRunWroteThem(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv(RunIDEnv, "run-4")
	root := repo(t, map[string]string{"a.go": "package a\n\nfunc F() {}\n\nfunc G() {}\n"})
	orig := read(t, root, "a.go")
	for _, tg := range []Target{{Root: root, Path: "a.go", Lang: "go", Qualified: "F"}, {Root: root, Path: "a.go", Lang: "go", Qualified: "G"}} {
		if _, err := Apply(tg, tg.Qualified+" does it."); err != nil {
			t.Fatal(err)
		}
	}
	user := read(t, root, "a.go") + "\n// the user's own line\n"
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte(user), 0o644); err != nil {
		t.Fatal(err)
	}
	kept, err := Restore("run-4", false)
	if err != nil || len(kept) != 1 || kept[0] != filepath.Join(root, "a.go") {
		t.Fatalf("kept %v, %v", kept, err)
	}
	if read(t, root, "a.go") != user {
		t.Fatal("the user's edit was overwritten")
	}
	saved := filepath.Join(state, "codedoc", "runs", "run-4", journalDir(root), "a.go")
	if b, err := os.ReadFile(saved); err != nil || string(b) != orig {
		t.Fatalf("journal not kept for the kept file: %q %v", b, err)
	}
}

func TestRestoreCanKeepTheJournal(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv(RunIDEnv, "run-5")
	root := repo(t, map[string]string{"a.go": "package a\n\nfunc F() {}\n"})
	orig := read(t, root, "a.go")
	if _, err := Apply(Target{Root: root, Path: "a.go", Lang: "go", Qualified: "F"}, "F does it."); err != nil {
		t.Fatal(err)
	}
	if kept, err := Restore("run-5", true); err != nil || len(kept) != 0 {
		t.Fatalf("kept %v, %v", kept, err)
	}
	if read(t, root, "a.go") != orig {
		t.Fatal("not restored")
	}
	if files, err := Files("run-5"); err != nil || len(files) != 1 {
		t.Fatalf("journal dropped: %v %v", files, err)
	}
}

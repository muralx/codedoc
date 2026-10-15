package write

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/muralx/codedoc/internal/testutil"
)

// TestMain keeps the per-file locks and journals out of the user's state
// directory (see testutil.IsolateEnv).
func TestMain(m *testing.M) {
	os.Exit(testutil.MainIsolated(m))
}

// withHook runs f at each stage of Apply for one test.
func withHook(t *testing.T, f func(stage, abs string)) {
	t.Helper()
	saved := hook
	hook = f
	t.Cleanup(func() { hook = saved })
}

func TestConcurrentAppliesToOneFileSerialize(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := repo(t, map[string]string{"a.go": "package a\n\nfunc F() {}\n\nfunc G() {}\n"})
	withHook(t, func(stage, _ string) {
		if stage == "planned" {
			time.Sleep(30 * time.Millisecond)
		}
	})
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, name := range []string{"F", "G"} {
		wg.Go(func() {
			_, errs[i] = Apply(Target{Root: root, Path: "a.go", Lang: "go", Qualified: name}, name+" does.")
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}
	got := read(t, root, "a.go")
	if !strings.Contains(got, "// F does.\nfunc F()") || !strings.Contains(got, "// G does.\nfunc G()") {
		t.Fatalf("a concurrent write was lost:\n%s", got)
	}
	locks, err := os.ReadDir(filepath.Join(os.Getenv("XDG_STATE_HOME"), "codedoc", "locks"))
	if err != nil || len(locks) != 1 {
		t.Fatalf("want one lock file in the state directory: %v %v", locks, err)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 1 {
		t.Fatalf("files left next to the source: %v", entries)
	}
}

func TestCrashBeforeRenameKeepsTheOriginal(t *testing.T) {
	orig := "package a\n\nfunc F() {}\n"
	root := repo(t, map[string]string{"a.go": orig})
	saved := renameFunc
	renameFunc = func(string, string) error { return errors.New("crashed") }
	t.Cleanup(func() { renameFunc = saved })
	if _, err := Apply(Target{Root: root, Path: "a.go", Lang: "go", Qualified: "F"}, "F does."); err == nil {
		t.Fatal("accepted")
	}
	if got := read(t, root, "a.go"); got != orig {
		t.Fatalf("original changed:\n%s", got)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 1 {
		t.Fatalf("temporary file left behind: %v", entries)
	}
}

func TestUndoLeavesANewerEdit(t *testing.T) {
	root := repo(t, map[string]string{"a.go": "package a\n\nfunc F() {}\n"})
	plan := func(tg Target, text string) (Edit, error) {
		e, err := Plan(tg, text)
		e.After = []byte("package a\n\n// F does something else.\nfunc F() {}\n")
		return e, err
	}
	edited := "package a\n\n// F does something else.\nfunc F() {}\n// editor\n"
	withHook(t, func(stage, abs string) {
		if stage == "written" {
			_ = os.WriteFile(abs, []byte(edited), 0o644)
		}
	})
	_, err := apply(plan, Target{Root: root, Path: "a.go", Lang: "go", Qualified: "F"}, "F does.")
	if err == nil || !strings.Contains(err.Error(), "changed after codedoc wrote it") {
		t.Fatalf("%v", err)
	}
	if got := read(t, root, "a.go"); got != edited {
		t.Fatalf("the newer edit was undone:\n%s", got)
	}
}

func TestApplyWritesThroughSymlinks(t *testing.T) {
	root := repo(t, map[string]string{"real.go": "package a\n\nfunc F() {}\n"})
	if err := os.Symlink("real.go", filepath.Join(root, "a.go")); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(Target{Root: root, Path: "a.go", Lang: "go", Qualified: "F"}, "F does."); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(filepath.Join(root, "a.go")); err != nil || fi.Mode().Type() != os.ModeSymlink {
		t.Fatalf("the symlink was replaced: %v", err)
	}
	if !strings.Contains(read(t, root, "real.go"), "// F does.") {
		t.Fatal("the link's target was not documented")
	}
}

func TestPlanThatKeepsMovingFails(t *testing.T) {
	root := repo(t, map[string]string{"p/a.go": "package p\n"})
	n := 0
	plan := func(tg Target, text string) (Edit, error) {
		n++
		e, err := Plan(tg, text)
		e.Path = filepath.Join("p", strings.Repeat("x", n)+".go")
		return e, err
	}
	_, err := apply(plan, Target{Root: root, Path: "p", Lang: "go", ModuleDir: "p"}, "Does p.")
	if err == nil || !strings.Contains(err.Error(), "kept changing") {
		t.Fatalf("%v", err)
	}
}

func TestApplyFailsWithoutALockDirectory(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	if err := os.WriteFile(filepath.Join(state, "codedoc"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	root := repo(t, map[string]string{"a.go": "package a\n\nfunc F() {}\n"})
	if _, err := Apply(Target{Root: root, Path: "a.go", Lang: "go", Qualified: "F"}, "F does."); err == nil {
		t.Fatal("wrote without the file's lock")
	}
}

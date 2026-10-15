package write

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratedRefused(t *testing.T) {
	root := repo(t, map[string]string{"a.go": "package a\n\nfunc F() {}\n"})
	_, err := Apply(Target{Root: root, Path: "a.go", Lang: "go", Qualified: "F", Generated: true}, "F.")
	if !errors.Is(err, ErrGenerated) || read(t, root, "a.go") != "package a\n\nfunc F() {}\n" {
		t.Fatalf("%v", err)
	}
}

func TestPlanErrors(t *testing.T) {
	root := repo(t, map[string]string{"a.go": "package a\n\nfunc F() {}\n"})
	for name, c := range map[string]struct {
		tg   Target
		text string
	}{
		"language":     {Target{Root: root, Path: "a.go", Lang: "cobol", Qualified: "F"}, "x"},
		"empty":        {Target{Root: root, Path: "a.go", Lang: "go", Qualified: "F"}, " \n\t\n"},
		"missing file": {Target{Root: root, Path: "b.go", Lang: "go", Qualified: "F"}, "x"},
	} {
		if _, err := Apply(c.tg, c.text); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestVerificationFailureRestores(t *testing.T) {
	orig := "package a\n\nfunc F() {}\n"
	for name, broken := range map[string]string{
		"syntax error": "package a\n\n// F does.\nfunc F( {}\n",
		"wrong doc":    "package a\n\n// F does something else.\nfunc F() {}\n",
		"no symbol":    "package a\n\n// G does.\nfunc G() {}\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := repo(t, map[string]string{"a.go": orig})
			plan := func(tg Target, text string) (Edit, error) {
				e, err := Plan(tg, text)
				e.After = []byte(broken)
				return e, err
			}
			_, err := apply(plan, Target{Root: root, Path: "a.go", Lang: "go", Qualified: "F"}, "F does.")
			if err == nil || !strings.Contains(err.Error(), "verification failed") {
				t.Fatalf("%v", err)
			}
			if got := read(t, root, "a.go"); got != orig {
				t.Fatalf("not restored:\n%s", got)
			}
		})
	}
}

func TestVerificationFailureRemovesCreatedFile(t *testing.T) {
	root := repo(t, map[string]string{"p/a.go": "package p\n"})
	plan := func(tg Target, text string) (Edit, error) {
		e, err := Plan(tg, text)
		e.After = []byte("package p\n")
		return e, err
	}
	if _, err := apply(plan, Target{Root: root, Path: "p", Lang: "go", ModuleDir: "p"}, "Does p."); err == nil {
		t.Fatal("accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "p", "doc.go")); !os.IsNotExist(err) {
		t.Fatalf("doc.go left behind: %v", err)
	}
}

func TestApplyKeepsMode(t *testing.T) {
	root := repo(t, map[string]string{"run.py": "def main():\n    pass\n"})
	p := filepath.Join(root, "run.py")
	if err := os.Chmod(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(Target{Root: root, Path: "run.py", Lang: "python", Qualified: "main"}, "Runs."); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != 0o755 {
		t.Fatalf("%v %v", fi.Mode(), err)
	}
}

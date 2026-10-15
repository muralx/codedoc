package gitmeta_test

import (
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/gitmeta"
)

func TestStatusPathsUnquotedWithBothSidesOfARename(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "old.go", "package a\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "one")
	git(t, dir, "mv", "old.go", "new.go")
	write(t, dir, "café.go", "package a\n")
	got, err := gitmeta.StatusPaths(dir)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != "café.go,new.go,old.go" {
		t.Fatalf("%q", got)
	}
	if _, err := gitmeta.StatusPaths(t.TempDir()); !errors.Is(err, gitmeta.ErrNotRepo) {
		t.Fatalf("outside git: %v", err)
	}
}

func TestOutputAndInput(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "a.go", "package a\n")
	git(t, dir, "add", "-A")
	if err := gitmeta.Input(dir, "subject\n\nbody\n", "-c", "user.email=t@x", "-c", "user.name=t",
		"commit", "-q", "-F", "-"); err != nil {
		t.Fatal(err)
	}
	out, err := gitmeta.Output(dir, "log", "-1", "--format=%s%n%b")
	if err != nil || out != "subject\nbody" {
		t.Fatalf("%q %v", out, err)
	}
	if err := gitmeta.Input(dir, "", "no-such-command"); err == nil || !strings.Contains(err.Error(), "no-such-command") {
		t.Fatalf("%v", err)
	}
}

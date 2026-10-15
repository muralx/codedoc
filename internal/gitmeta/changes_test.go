package gitmeta_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/gitmeta"
)

func TestChangesSinceDetectsRenames(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	body := strings.Repeat("a line long enough to make the rename similarity obvious\n", 5)
	write(t, dir, "pkg/old.txt", body)
	write(t, dir, "pkg/keep.txt", "k\n")
	write(t, dir, "pkg/gone.txt", "g\n")
	base := commitAt(t, dir, jan, "first")
	git(t, dir, "mv", "pkg/old.txt", "pkg/new.txt")
	write(t, dir, "pkg/keep.txt", "k2\n")
	git(t, dir, "rm", "-q", "pkg/gone.txt")
	write(t, dir, "pkg/untracked.txt", "u\n")
	got, err := gitmeta.ChangesSince(filepath.Join(dir, "pkg"), base)
	if err != nil {
		t.Fatal(err)
	}
	want := []gitmeta.Changed{
		{Status: 'D', Path: "gone.txt"},
		{Status: 'M', Path: "keep.txt"},
		{Status: 'R', Path: "new.txt", OldPath: "old.txt"},
		{Status: 'A', Path: "untracked.txt"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestBlobsReadsFilesAtOneRevision(t *testing.T) {
	dir, c := historyRepo(t)
	b, err := gitmeta.OpenBlobs(filepath.Join(dir, "pkg"), c[0])
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := b.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	for i := 0; i < 2; i++ {
		src, ok, err := b.Read("f.txt")
		if err != nil || !ok || string(src) != "a\nb\nc\n" {
			t.Fatalf("read %d: %q %v %v", i, src, ok, err)
		}
	}
	if _, ok, err := b.Read("."); ok || err != nil {
		t.Fatalf("a directory is no file: %v %v", ok, err)
	}
	if _, ok, err := b.Read("nope.txt"); ok || err != nil {
		t.Fatalf("missing path: %v %v", ok, err)
	}
	if _, err := gitmeta.OpenBlobs(t.TempDir(), "HEAD"); err == nil {
		t.Fatal("blobs outside git")
	}
}

package gitmeta_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/muralx/codedoc/internal/gitmeta"
)

// commitAt commits everything under dir with fixed author and committer
// dates and returns the new HEAD.
func commitAt(t *testing.T, dir string, at time.Time, msg string) string {
	t.Helper()
	date := at.UTC().Format(time.RFC3339)
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", msg}} {
		cmd := exec.Command("git", append([]string{"-c", "user.email=t@x", "-c", "user.name=Tess"}, args...)...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return headOf(t, dir)
}

var (
	jan = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	feb = time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	mar = time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
)

// historyRepo builds pkg/f.txt over three commits (jan, feb, mar) in a
// repo whose root holds a sibling directory, and returns the repo root and
// the commits.
func historyRepo(t *testing.T) (string, []string) {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "pkg/f.txt", "a\nb\nc\n")
	write(t, dir, "other/o.txt", "o\n")
	c1 := commitAt(t, dir, jan, "first")
	write(t, dir, "other/o.txt", "o2\n")
	c2 := commitAt(t, dir, feb, "second")
	write(t, dir, "pkg/f.txt", "a\nB\nc\nd\n")
	c3 := commitAt(t, dir, mar, "third")
	return dir, []string{c1, c2, c3}
}

func TestBlame(t *testing.T) {
	dir, _ := historyRepo(t)
	write(t, dir, "pkg/f.txt", "a\nB\nX\nd\n")
	for _, d := range []string{dir, filepath.Join(dir, "pkg")} {
		p := "f.txt"
		if d == dir {
			p = "pkg/f.txt"
		}
		got, err := gitmeta.Blame(d, p)
		if err != nil {
			t.Fatal(err)
		}
		want := map[int]int64{1: jan.Unix(), 2: mar.Unix(), 3: gitmeta.Uncommitted, 4: mar.Unix()}
		if len(got) != len(want) {
			t.Fatalf("%s: %v", d, got)
		}
		for line, at := range want {
			if got[line] != at {
				t.Errorf("%s line %d: %d, want %d", d, line, got[line], at)
			}
		}
	}
}

func TestBlameUntrackedAndStaged(t *testing.T) {
	dir, _ := historyRepo(t)
	write(t, dir, "pkg/new.txt", "n\n")
	if _, err := gitmeta.Blame(dir, "pkg/new.txt"); !errors.Is(err, gitmeta.ErrUntracked) {
		t.Fatalf("untracked: %v", err)
	}
	git(t, dir, "add", "pkg/new.txt")
	got, err := gitmeta.Blame(dir, "pkg/new.txt")
	if err != nil || got[1] != gitmeta.Uncommitted {
		t.Fatalf("staged: %v %v", got, err)
	}
	empty := t.TempDir()
	git(t, empty, "init", "-q")
	write(t, empty, "a.txt", "a\n")
	git(t, empty, "add", "a.txt")
	if _, err := gitmeta.Blame(empty, "a.txt"); !errors.Is(err, gitmeta.ErrUntracked) {
		t.Fatalf("no commits: %v", err)
	}
}

func TestLogRange(t *testing.T) {
	dir, c := historyRepo(t)
	got, err := gitmeta.LogRange(filepath.Join(dir, "pkg"), "f.txt", 2, 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []gitmeta.Commit{
		{Commit: c[2], At: mar.Unix(), Author: "Tess", Subject: "third"},
		{Commit: c[0], At: jan.Unix(), Author: "Tess", Subject: "first"},
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("%+v", got)
	}
	got, err = gitmeta.LogRange(dir, "pkg/f.txt", 1, 1, 1)
	if err != nil || len(got) != 1 || got[0].Commit != c[0] {
		t.Fatalf("limit: %+v %v", got, err)
	}
	if _, err := gitmeta.LogRange(dir, "pkg/f.txt", 3, 2, 0); err == nil {
		t.Fatal("inverted range accepted")
	}
	if _, err := gitmeta.LogRange(dir, "pkg/none.txt", 1, 1, 0); err == nil {
		t.Fatal("missing file accepted")
	}
}

func TestShow(t *testing.T) {
	dir, c := historyRepo(t)
	pkg := filepath.Join(dir, "pkg")
	got, err := gitmeta.Show(pkg, c[0], "f.txt")
	if err != nil || string(got) != "a\nb\nc\n" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := gitmeta.Show(pkg, c[0], "missing.txt"); !errors.Is(err, gitmeta.ErrNoPath) {
		t.Fatalf("missing path: %v", err)
	}
	if _, err := gitmeta.Show(pkg, "no-such-ref", "f.txt"); err == nil || errors.Is(err, gitmeta.ErrNoPath) {
		t.Fatalf("unknown ref: %v", err)
	}
	if _, err := gitmeta.Show(t.TempDir(), "HEAD", "f.txt"); !errors.Is(err, gitmeta.ErrNotRepo) {
		t.Fatalf("not a repo: %v", err)
	}
}

func TestResolveSinceRef(t *testing.T) {
	dir, c := historyRepo(t)
	got, err := gitmeta.ResolveSince(dir, "HEAD~1")
	if err != nil || got != c[1] {
		t.Fatalf("%s %v", got, err)
	}
	got, err = gitmeta.ResolveSince(dir, c[0][:8])
	if err != nil || got != c[0] {
		t.Fatalf("short hash: %s %v", got, err)
	}
	if _, err := gitmeta.ResolveSince(dir, "nope"); err == nil {
		t.Fatal("unknown ref accepted")
	}
}

func TestResolveSinceDuration(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "a.txt", "1\n")
	old := commitAt(t, dir, now.Add(-30*24*time.Hour), "old")
	write(t, dir, "a.txt", "2\n")
	mid := commitAt(t, dir, now.Add(-5*24*time.Hour), "mid")
	write(t, dir, "a.txt", "3\n")
	recent := commitAt(t, dir, now.Add(-2*time.Hour), "recent")
	for since, want := range map[string]string{"3d": mid, "12h": mid, "2w": old, "1h": recent} {
		got, err := gitmeta.ResolveSince(dir, since)
		if err != nil || got != want {
			t.Errorf("%s: %s %v, want %s", since, got, err, want)
		}
	}
	got, err := gitmeta.ResolveSince(dir, "8w")
	if err != nil || len(got) != 40 || got == old {
		t.Fatalf("before every commit: %s %v", got, err)
	}
	if out, _ := gitmeta.Show(dir, got, "a.txt"); out != nil {
		t.Fatal("the empty tree has files")
	}
}

func TestResolveSinceEmptyRepo(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	got, err := gitmeta.ResolveSince(dir, "1d")
	if err != nil || len(got) != 40 {
		t.Fatalf("%s %v", got, err)
	}
}

func TestRecentCommits(t *testing.T) {
	dir, c := historyRepo(t)
	got, err := gitmeta.RecentCommits(dir, 2)
	if err != nil || len(got) != 2 || got[0].Commit != c[2] || got[1].Commit != c[1] || got[1].Subject != "second" {
		t.Fatalf("%+v %v", got, err)
	}
	got, err = gitmeta.RecentCommits(filepath.Join(dir, "pkg"), 10)
	if err != nil || len(got) != 2 || got[0].Commit != c[2] || got[1].Commit != c[0] {
		t.Fatalf("subdirectory: %+v %v", got, err)
	}
	empty := t.TempDir()
	git(t, empty, "init", "-q")
	if got, err := gitmeta.RecentCommits(empty, 5); err != nil || len(got) != 0 {
		t.Fatalf("empty: %v %v", got, err)
	}
}

func TestChangedSinceAndShowTree(t *testing.T) {
	dir, commits := historyRepo(t)
	write(t, dir, "pkg/new.txt", "n\n")
	write(t, dir, "other/o.txt", "o3\n")
	got, err := gitmeta.ChangedSince(dir, commits[1])
	if err != nil {
		t.Fatal(err)
	}
	if want := "other/o.txt,pkg/f.txt,pkg/new.txt"; strings.Join(got, ",") != want {
		t.Fatalf("got %v, want %s", got, want)
	}
	got, err = gitmeta.ChangedSince(filepath.Join(dir, "pkg"), commits[2])
	if err != nil || strings.Join(got, ",") != "new.txt" {
		t.Fatalf("subdirectory: %v %v", got, err)
	}
	empty, err := gitmeta.ResolveSince(dir, "5000d")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gitmeta.Show(dir, empty, "pkg/f.txt"); !errors.Is(err, gitmeta.ErrNoPath) {
		t.Fatalf("empty tree: %v", err)
	}
	if _, err := gitmeta.ChangedSince(dir, "nope"); err == nil {
		t.Fatal("unknown ref")
	}
}

func TestIsTracked(t *testing.T) {
	dir, _ := historyRepo(t)
	write(t, dir, "pkg/untracked.txt", "u\n")
	for p, want := range map[string]bool{"pkg/f.txt": true, "pkg/untracked.txt": false} {
		if got, err := gitmeta.IsTracked(dir, p); err != nil || got != want {
			t.Errorf("%s: %v %v", p, got, err)
		}
	}
}

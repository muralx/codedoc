package gitmeta_test

import (
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/gitmeta"
)

func TestBlameFollowsLinesMovedBetweenFiles(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	block := strings.Repeat("// a long enough line of moved text for copy detection to notice\n", 4)
	write(t, dir, "a.txt", "head\n"+block)
	commitAt(t, dir, jan, "first")
	write(t, dir, "a.txt", "head\n")
	write(t, dir, "b.txt", block)
	commitAt(t, dir, mar, "move")
	got, err := gitmeta.Blame(dir, "b.txt")
	if err != nil {
		t.Fatal(err)
	}
	for line := 1; line <= 4; line++ {
		if got[line] != jan.Unix() {
			t.Fatalf("line %d: %d, want the time of its origin %d", line, got[line], jan.Unix())
		}
	}
}

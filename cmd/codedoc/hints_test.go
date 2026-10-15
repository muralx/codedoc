package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/session"
)

// cliContinue matches the command a cut body ends with: the arguments
// after "codedoc".
var cliContinue = regexp.MustCompile(`(?m)^\d+ more lines: codedoc (lines) (\S+) (\d+-\d+)$`)

// bodyLine matches a numbered body line.
var bodyLine = regexp.MustCompile(`(?m)^\d+\t.*$`)

func TestCLIContinueCommandsPrintAWholeFilePieceByPiece(t *testing.T) {
	repos := e2eWorkspace(t)
	t.Setenv(session.HeadlessEnv, "1")
	src, err := os.ReadFile(filepath.Join(repos["go-lib"], "store", "store.go"))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Split(strings.TrimSuffix(string(src), "\n"), "\n")
	// From a directory outside every repo, as an agent may be.
	t.Chdir(t.TempDir())
	out := cliOK(t, []string{"lines", "go-lib:store/store.go", "1-999", "--budget", "500", "--workspace", "fx"})
	var got []string
	for range 200 {
		for _, l := range bodyLine.FindAllString(out, -1) {
			got = append(got, l[strings.Index(l, "\t")+1:])
		}
		m := cliContinue.FindStringSubmatch(out)
		if m == nil {
			break
		}
		out = cliOK(t, []string{m[1], m[2], m[3], "--budget", "500", "--workspace", "fx"})
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") || len(want) < 8 {
		t.Fatalf("got %d lines, want %d:\n%s", len(got), len(want), strings.Join(got, "\n"))
	}
}

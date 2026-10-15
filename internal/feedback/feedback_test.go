package feedback_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/muralx/codedoc/internal/feedback"
	"github.com/muralx/codedoc/internal/testutil"
)

// TestMain keeps the tests out of the user's real state directory (see
// testutil.IsolateEnv).
func TestMain(m *testing.M) {
	os.Exit(testutil.MainIsolated(m))
}

var (
	old    = feedback.Entry{Time: "2026-03-01T10:00:00Z", Workspace: "w", Repo: "r", Head: "main@abc", Text: "old", Key: "r:go:m:F"}
	recent = feedback.Entry{Time: "2026-03-03T10:00:00Z", Text: "new", Path: "a.go", Cmd: "codedoc symbol F", Dirty: true,
		IndexedHash: "1", CurrentHash: "2"}
)

// filed appends old and recent to a fresh feedback file and returns its
// path.
func filed(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	for _, e := range []feedback.Entry{old, recent} {
		if err := feedback.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	p, err := feedback.File()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// list returns the entries filed since a time.
func list(t *testing.T, since time.Time) []feedback.Entry {
	t.Helper()
	got, err := feedback.List(since)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestAnEmptyStateHoldsNoFeedback(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if got := list(t, time.Time{}); len(got) != 0 {
		t.Fatalf("no file: %v", got)
	}
	if n, err := feedback.Clear(); err != nil || n != 0 {
		t.Fatalf("clear without a file: %d %v", n, err)
	}
}

func TestAppendWritesOneJSONLinePerEntry(t *testing.T) {
	p := filed(t)
	if !strings.HasSuffix(p, filepath.Join("codedoc", "feedback.jsonl")) {
		t.Fatalf("file = %q", p)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], `{"time":`) {
		t.Fatalf("file content:\n%s", raw)
	}
	if strings.Contains(lines[0], "indexed_hash") {
		t.Errorf("an entry without a file carries no hash: %s", lines[0])
	}
}

func TestListFiltersBySinceAndSkipsOtherLines(t *testing.T) {
	p := filed(t)
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("not json\n{}\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if all := list(t, time.Time{}); !slices.Equal(all, []feedback.Entry{old, recent}) {
		t.Fatalf("list = %+v", all)
	}
	if since := list(t, time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)); !slices.Equal(since, []feedback.Entry{recent}) {
		t.Fatalf("since = %+v", since)
	}
}

func TestClearEmptiesTheFile(t *testing.T) {
	filed(t)
	if n, err := feedback.Clear(); err != nil || n != 2 {
		t.Fatalf("clear = %d %v", n, err)
	}
	if got := list(t, time.Time{}); len(got) != 0 {
		t.Fatalf("after clear: %+v", got)
	}
}

// failing runs the three operations and reports which succeeded.
func failing() []string {
	var ok []string
	if err := feedback.Append(feedback.Entry{Text: "x"}); err == nil {
		ok = append(ok, "append")
	}
	if _, err := feedback.List(time.Time{}); err == nil {
		ok = append(ok, "list")
	}
	if _, err := feedback.Clear(); err == nil {
		ok = append(ok, "clear")
	}
	return ok
}

func TestErrorsSurface(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	// The state directory is a file: nothing can be created below it.
	if err := os.WriteFile(filepath.Join(state, "codedoc"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if ok := failing(); len(ok) != 0 {
		t.Errorf("under a file, succeeded: %v", ok)
	}
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "")
	if _, err := feedback.File(); err == nil {
		t.Skip("the home directory resolves without HOME on this system")
	}
	if ok := failing(); len(ok) != 0 {
		t.Errorf("without a state directory, succeeded: %v", ok)
	}
}

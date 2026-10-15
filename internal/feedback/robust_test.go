package feedback_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/muralx/codedoc/internal/feedback"
)

// appendRaw adds raw bytes to the feedback file.
func appendRaw(t *testing.T, p string, raw []byte) {
	t.Helper()
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// size returns the size of the feedback file.
func size(t *testing.T, p string) int64 {
	t.Helper()
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return st.Size()
}

func TestAppendCapsTheTextAndTheFields(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	long := "x" + strings.Repeat("é", feedback.MaxText) // over twice MaxText bytes; the cuts fall inside an é
	if err := feedback.Append(feedback.Entry{Time: old.Time, Text: long, Key: long, Path: long, Cmd: long, Repo: long}); err != nil {
		t.Fatal(err)
	}
	got := list(t, time.Time{})
	if len(got) != 1 {
		t.Fatalf("list = %d entries", len(got))
	}
	e := got[0]
	if len(e.Text) > feedback.MaxText || len(e.Text) < feedback.MaxText-4 || !strings.HasSuffix(e.Text, "é"+feedback.Truncated) {
		t.Errorf("text of %d bytes", len(e.Text))
	}
	for name, v := range map[string]string{"key": e.Key, "path": e.Path, "cmd": e.Cmd, "repo": e.Repo} {
		if len(v) > feedback.MaxField || !strings.HasSuffix(v, "é"+feedback.Truncated) {
			t.Errorf("%s of %d bytes", name, len(v))
		}
	}
	if short := feedback.Clamp(feedback.Entry{Text: "short", Key: "k"}); short.Text != "short" || short.Key != "k" {
		t.Errorf("a short entry is kept as it is: %+v", short)
	}
}

// TestListReadsPastLongAndGarbledLines checks that a line longer than
// any entry, binary garbage and a last line without a newline cost only
// themselves: the entries around them are listed.
func TestListReadsPastLongAndGarbledLines(t *testing.T) {
	p := filed(t)
	appendRaw(t, p, append([]byte(strings.Repeat("x", 2<<20)), '\n'))
	appendRaw(t, p, []byte{0xff, 0xfe, 0x00, '{', '\n'})
	if err := feedback.Append(recent); err != nil {
		t.Fatal(err)
	}
	appendRaw(t, p, []byte(`{"time":"2026-03-04T10:00:00Z","text":"cut sho`))
	if all := list(t, time.Time{}); !slices.Equal(all, []feedback.Entry{old, recent, recent}) {
		t.Fatalf("list = %+v", all)
	}
	// A report filed after a line that lost its line break starts its own.
	if err := feedback.Append(old); err != nil {
		t.Fatal(err)
	}
	if all := list(t, time.Time{}); !slices.Equal(all, []feedback.Entry{old, recent, recent, old}) {
		t.Fatalf("list after a cut line = %+v", all)
	}
}

func TestClearTruncatesAFileItCannotRead(t *testing.T) {
	p := filed(t)
	appendRaw(t, p, []byte(strings.Repeat("\x00garbage", 1<<10)))
	if n, err := feedback.Clear(); err != nil || n != 2 {
		t.Fatalf("clear = %d %v", n, err)
	}
	if size(t, p) != 0 {
		t.Fatal("a garbled file is not truncated")
	}
	// A file that cannot be opened for reading is emptied all the same.
	appendRaw(t, p, []byte("{}\n"))
	if err := os.Chmod(p, 0o200); err != nil {
		t.Fatal(err)
	}
	if n, err := feedback.Clear(); err != nil || n != 0 {
		t.Fatalf("clear of an unreadable file = %d %v", n, err)
	}
	if size(t, p) != 0 {
		t.Fatal("an unreadable file is not truncated")
	}
}

// TestAppendRotatesAFullFile fills the file to its cap: the next report
// drops the oldest ones, keeps the newest whole and leaves the file at
// about half the cap.
func TestAppendRotatesAFullFile(t *testing.T) {
	p := filed(t)
	filler := feedback.Entry{Time: "2026-03-02T10:00:00Z", Text: strings.Repeat("f", 4<<10)}
	for size(t, p) < feedback.MaxFileSize-(8<<10) {
		if err := feedback.Append(filler); err != nil {
			t.Fatal(err)
		}
	}
	before := list(t, time.Time{})
	last := feedback.Entry{Time: "2026-03-05T10:00:00Z", Text: strings.Repeat("l", 8<<10)}
	if err := feedback.Append(last); err != nil {
		t.Fatal(err)
	}
	after := list(t, time.Time{})
	if st, err := os.Stat(p); err != nil || st.Size() > feedback.MaxFileSize/2+(16<<10) || st.Mode().Perm() != 0o600 {
		t.Fatalf("rotated file: %v %v", st, err)
	}
	if len(after) >= len(before) || len(after) < len(before)/3 || after[len(after)-1] != last || after[0] != filler {
		t.Fatalf("kept %d of %d entries", len(after), len(before))
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(p), "*")); len(left) != 1 {
		t.Fatalf("temporary files left: %v", left)
	}
}

// TestRotationDropsAGarbledTail checks that a full file holding no line
// break at all (nothing to keep whole) is emptied by the next report.
func TestRotationDropsAGarbledTail(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := feedback.Append(old); err != nil {
		t.Fatal(err)
	}
	p, err := feedback.File()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(strings.Repeat("x", feedback.MaxFileSize)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := feedback.Append(recent); err != nil {
		t.Fatal(err)
	}
	if got := list(t, time.Time{}); !slices.Equal(got, []feedback.Entry{recent}) || size(t, p) > 1<<10 {
		t.Fatalf("list = %+v, size %d", got, size(t, p))
	}
}

// TestAFullFileThatCannotBeRotatedRefusesTheReport checks that the cap
// holds when the rotated copy cannot be written: the report is an error
// and the file is left as it was.
func TestAFullFileThatCannotBeRotatedRefusesTheReport(t *testing.T) {
	p := filed(t)
	if err := os.WriteFile(p, []byte(strings.Repeat("x", feedback.MaxFileSize)), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(p)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if err := feedback.Append(recent); err == nil {
		t.Fatal("a report was added to a full file")
	}
	if size(t, p) != feedback.MaxFileSize {
		t.Fatalf("size = %d", size(t, p))
	}
}

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/feedback"
	"github.com/muralx/codedoc/internal/session"
)

// fileReports files n short reports outside every workspace, in a fresh
// state directory, and returns the path of the feedback file.
func fileReports(t *testing.T, n int) string {
	t.Helper()
	file := feedbackState(t)
	t.Chdir(t.TempDir())
	for i := range n {
		cliOK(t, []string{"feedback", fmt.Sprintf("report %02d %s", i, strings.Repeat("x", 80))})
	}
	return file
}

// TestFeedbackListHonoursBudgetAndOffset checks that the list is cut
// and continued like a query answer.
func TestFeedbackListHonoursBudgetAndOffset(t *testing.T) {
	fileReports(t, 30)
	out := cliOK(t, []string{"feedback", "--list", "--budget", "600"}, "== feedback (30)\n", "  report 00 x")
	if len(out) > 600 || !strings.Contains(out, " more: codedoc feedback --budget 600 --list --offset 3\n") || strings.Contains(out, "report 29") {
		t.Fatalf("%d bytes:\n%s", len(out), out)
	}
	out = cliOK(t, []string{"feedback", "--list", "--offset", "28"}, "== feedback (30)\n", "report 28", "report 29")
	if strings.Contains(out, "report 27") || strings.Count(out, "\n") != 3 {
		t.Fatalf("got:\n%s", out)
	}
	cliOK(t, []string{"feedback", "--list", "--offset", "40"}, "== feedback (30)\noffset 40 is past the end (total 30)\n")
	if all := cliOK(t, []string{"feedback", "--list", "--budget", "0"}); strings.Count(all, "\n") != 31 {
		t.Fatalf("unlimited:\n%s", all)
	}
	for _, args := range [][]string{{"feedback", "--list", "--offset", "-1"}, {"feedback", "--list", "--budget", "-1"}} {
		if out, err := runCLI(t, args...); err == nil {
			t.Errorf("%v accepted:\n%s", args, out)
		}
	}
}

// TestFeedbackListPagesJSONAndWritesOut checks the JSON page of the
// list, which carries each stored report under extra, and --out.
func TestFeedbackListPagesJSONAndWritesOut(t *testing.T) {
	fileReports(t, 30)
	js := cliOK(t, []string{"feedback", "--list", "--format", "json", "--budget", "1500", "--offset", "10"})
	var got struct {
		Sections []struct {
			Entries []struct {
				Key   string
				Extra feedback.Entry
			}
			Omitted    int
			NextOffset int `json:"next_offset"`
		}
	}
	if err := json.Unmarshal([]byte(js), &got); err != nil || len(js) > 1500 || len(got.Sections) != 1 {
		t.Fatalf("%v: %s", err, js)
	}
	sec := got.Sections[0]
	if n := len(sec.Entries); n == 0 || sec.Omitted != 20-n || sec.NextOffset != 10+n ||
		!strings.HasPrefix(sec.Entries[0].Extra.Text, "report 10 ") || sec.Entries[0].Key != sec.Entries[0].Extra.Time {
		t.Fatalf("json page: %s", js)
	}
	full := filepath.Join(t.TempDir(), "all.txt")
	out := cliOK(t, []string{"feedback", "--list", "--out", full}, "== feedback (30)\n", "full result: "+full+"\n")
	if raw, err := os.ReadFile(full); err != nil || strings.Count(string(raw), "\n") != 31 || len(out) > 700 {
		t.Fatalf("--out: %v\n%s\n%s", err, out, raw)
	}
}

// TestFeedbackKeepsALongReportBounded files a text far over the cap:
// it is stored cut, and its list line is short enough for the default
// budget while --format json carries the stored text.
func TestFeedbackKeepsALongReportBounded(t *testing.T) {
	file := feedbackState(t)
	t.Chdir(t.TempDir())
	cliOK(t, []string{"feedback", strings.Repeat("long text ", 4<<10), "--cmd", strings.Repeat("c", 4<<10)})
	if st, err := os.Stat(file); err != nil || st.Size() > feedback.MaxText+2*feedback.MaxField {
		t.Fatalf("stored: %v %v", st, err)
	}
	cliOK(t, []string{"feedback", "second"})
	out := cliOK(t, []string{"feedback", "--list"}, "== feedback (2)\n", "long text long text", "  second  [codedoc ",
		fmt.Sprintf("[text cut: %d bytes, whole with --format json --out FILE]", feedback.MaxText))
	if len(out) > 3000 {
		t.Fatalf("list of %d bytes:\n%s", len(out), out)
	}
	cliOK(t, []string{"feedback", "--list", "--format", "json", "--budget", "0"}, "long text "+feedback.Truncated+`","time":`,
		"ccc"+feedback.Truncated+`","dirty":false`)
}

// TestFeedbackSurvivesAGarbledFile checks --list and --clear on a file
// holding an over-long line and bytes that are no JSON.
func TestFeedbackSurvivesAGarbledFile(t *testing.T) {
	file := fileReports(t, 2)
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	garbled := strings.Repeat("y", 2<<20) + "\n\x00\xff{\n" + string(raw) + `{"time":"cut`
	if err := os.WriteFile(file, []byte(garbled), 0o600); err != nil {
		t.Fatal(err)
	}
	cliOK(t, []string{"feedback", "--list"}, "== feedback (2)\n", "report 00", "report 01")
	if out := cliOK(t, []string{"feedback", "--clear", "--verbose"}); out != "feedback cleared (2 reports)\n" {
		t.Fatalf("got %q", out)
	}
	if raw, _ := os.ReadFile(file); len(raw) != 0 {
		t.Fatalf("not cleared: %d bytes", len(raw))
	}
}

// TestMCPFeedbackListIsBudgeted checks that the tool pages the list:
// the default budget of the server cuts it and offset continues it.
func TestMCPFeedbackListIsBudgeted(t *testing.T) {
	e2eWorkspace(t)
	t.Setenv(session.HeadlessEnv, "1")
	feedbackState(t)
	cs, _ := mcpSession(t)
	for i := range 80 {
		text := fmt.Sprintf("report %02d %s", i, strings.Repeat("x", 80))
		if got, isErr := mcpCall(t, cs, "codedoc_feedback", map[string]any{"text": text, "workspace": "fx"}); isErr {
			t.Fatal(got)
		}
	}
	got, isErr := mcpCall(t, cs, "codedoc_feedback", map[string]any{"list": true})
	if isErr || len(got) > 6000 || !strings.Contains(got, "== feedback (80)\n") || !strings.Contains(got, ` more: codedoc_feedback {"budget":6000,"list":true,"offset":`) {
		t.Fatalf("%v, %d bytes:\n%s", isErr, len(got), got)
	}
	got, isErr = mcpCall(t, cs, "codedoc_feedback", map[string]any{"list": true, "offset": 79, "budget": 500})
	if isErr || !strings.Contains(got, "report 79") || strings.Contains(got, "report 78") {
		t.Fatalf("%v\n%s", isErr, got)
	}
}

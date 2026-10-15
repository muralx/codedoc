package mcpserver

import (
	"fmt"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/query"
)

// A write prints nothing when it succeeds, and a tool result must have
// content: the answer is "ok".
func TestSilentWritesAnswerOk(t *testing.T) {
	headless(t)
	rec := &recorder{}
	cs, _ := connect(t, Options{Exec: rec.exec, Log: &lines{}})
	for tool, in := range map[string]map[string]any{
		"codedoc_doc":      {"action": "set", "target": "S", "text": "S does it."},
		"codedoc_feedback": {"text": "refs misses a caller"},
		"codedoc_deps":     {"action": "index"},
	} {
		if text, isErr := callText(t, cs, tool, in); isErr || text != "ok" {
			t.Errorf("%s: %v %q", tool, isErr, text)
		}
	}
	for tool, in := range map[string]map[string]any{
		"codedoc_find":     {"pattern": "Nope"},
		"codedoc_feedback": {"list": true},
		"codedoc_deps":     {"action": "list"},
	} {
		if text, isErr := callText(t, cs, tool, in); isErr || text != "" {
			t.Errorf("%s asks, and an empty answer is not ok: %v %q", tool, isErr, text)
		}
	}
}

func TestDocsInputRunsOneBatch(t *testing.T) {
	headless(t)
	rec := &recorder{}
	cs, _ := connect(t, Options{Exec: rec.exec, Log: &lines{}})
	docs := []map[string]any{{"address": " a.go#A ", "text": "A does it.\n\nMore of A."}, {"address": "r:dir/", "text": "Package dir."}}
	if text, isErr := callText(t, cs, "codedoc_doc", map[string]any{"action": "set", "docs": docs, "repo": "r"}); isErr || text != "ok" {
		t.Fatalf("%v %q", isErr, text)
	}
	if got := strings.Join(rec.args[0], " "); got != "doc set --batch --repo=r --budget=6000" {
		t.Errorf("args: %s", got)
	}
	if want := "=== a.go#A\nA does it.\n\nMore of A.\n=== r:dir/\nPackage dir.\n"; rec.stdin[0] != want {
		t.Errorf("stdin: %q", rec.stdin[0])
	}
	recs, failed := query.ParseDocBatch(rec.stdin[0])
	if len(recs) != 2 || len(failed) != 0 || recs[0].Text != "A does it.\n\nMore of A." {
		t.Errorf("the CLI reads it back as %q %q", recs, failed)
	}
	// The failures a batch listed are the answer, without another line.
	rec.stderr, rec.err = "Nope: not found; look it up\n", fmt.Errorf("1 of 2 docs failed: %w", query.ErrReported)
	if text, isErr := callText(t, cs, "codedoc_doc", map[string]any{"action": "set", "docs": docs}); !isErr || text != rec.stderr {
		t.Errorf("%v %q", isErr, text)
	}
}

func TestDocsInputRefusesWhatABatchCannotCarry(t *testing.T) {
	headless(t)
	rec := &recorder{}
	cs, _ := connect(t, Options{Exec: rec.exec, Log: &lines{}})
	doc := func(addr, text string) []map[string]any { return []map[string]any{{"address": addr, "text": text}} }
	for _, c := range []struct {
		in   map[string]any
		want string
	}{
		{map[string]any{"docs": doc("A", "x"), "target": "A"}, "inputs target and text do not go with docs: put every doc in docs"},
		{map[string]any{"docs": doc(" ", "x")}, "docs[0]: address must be one non-empty line"},
		{map[string]any{"docs": doc("A\nB", "x")}, "docs[0]: address must be one non-empty line"},
		{map[string]any{"docs": doc("A", "=== B\nx")}, `docs[0] (A): a line of the text starts with "=== ", which docs cannot carry; ` +
			"set that doc alone with target and text"},
		{map[string]any{"docs": doc("A", "x\n=== B")}, `docs[0] (A): a line of the text starts with "=== ", which docs cannot carry; ` +
			"set that doc alone with target and text"},
	} {
		c.in["action"] = "set"
		if text, isErr := callText(t, cs, "codedoc_doc", c.in); !isErr || text != c.want {
			t.Errorf("%v: %v %q", c.in, isErr, text)
		}
	}
	if len(rec.args) != 0 {
		t.Fatalf("executed %v", rec.args)
	}
}

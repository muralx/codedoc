package main

import (
	"strings"
	"testing"
)

func TestSearchCommand(t *testing.T) {
	t.Setenv("CODEDOC_EMBEDDER", "fake")
	e2eWorkspace(t)
	out, err := runCLI(t, "status", "--workspace", "fx", "--repo", "go-lib")
	if err != nil || !strings.Contains(out, " vectors:18/18 ") {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err = runCLI(t, "search", "answers", "lookups", "--workspace", "fx", "--limit", "2")
	if err != nil || !strings.Contains(out, "== search \"answers lookups\" (6)\nservice/service.go\n  Service  :11  ") ||
		!strings.Contains(out, "\n4 more: codedoc search answers lookups --limit 2 --workspace fx --offset 2\n") ||
		strings.Contains(out, "text search only") {
		t.Fatalf("%v\n%s", err, out)
	}
	// The next page reports the same total.
	out, err = runCLI(t, "search", "answers", "lookups", "--workspace", "fx", "--limit", "2", "--offset", "2")
	if err != nil || !strings.Contains(out, "== search \"answers lookups\" (6)\n") ||
		!strings.Contains(out, "\n2 more: codedoc search answers lookups --limit 2 --workspace fx --offset 4\n") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestSearchTextOnlyWhenModelUnavailable(t *testing.T) {
	t.Setenv("CODEDOC_MODEL_DIR", t.TempDir())
	t.Setenv("CODEDOC_EMBEDDER", "")
	setupWorkspace(t)
	out, err := runCLI(t, "search", "store", "--workspace", "fx")
	if err != nil || !strings.Contains(out, "text search only: embedding model unavailable") ||
		!strings.Contains(out, "embeddings disabled") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestExplainFlowCommands(t *testing.T) {
	e2eWorkspace(t)
	out, err := runCLI(t, "explain", "MemStore.Get", "--workspace", "fx")
	if err != nil || !strings.Contains(out, "== explain MemStore.Get (5)") {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err = runCLI(t, "flow", "go-app:go:github.com/fixture/app:main", "--workspace", "fx", "--documented-only")
	if err != nil || !strings.Contains(out, "== flow from main (5)") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestSearchNoteNamesWhyItIsTextOnly(t *testing.T) {
	setupWorkspace(t)
	t.Setenv("CODEDOC_MODEL_DIR", "")
	t.Setenv("CODEDOC_EMBEDDER", "off")
	out, err := runCLI(t, "search", "store", "--workspace", "fx")
	if err != nil || !strings.Contains(out, "text search only: embeddings disabled (CODEDOC_EMBEDDER=off)") {
		t.Fatalf("%v\n%s", err, out)
	}
	t.Setenv("CODEDOC_EMBEDDER", "")
	out, err = runCLI(t, "search", "store", "--workspace", "fx")
	if err != nil || !strings.Contains(out, "text search only: no embedding model configured") {
		t.Fatalf("%v\n%s", err, out)
	}
}

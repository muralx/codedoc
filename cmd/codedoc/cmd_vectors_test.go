package main

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/testutil"
)

// TestMain turns embeddings off unless a test asks for them, so a model
// configured on the host (CODEDOC_MODEL_DIR) does not change answers.
func TestMain(m *testing.M) {
	if os.Getenv(stubClaudeEnv) != "" {
		os.Exit(stubClaude(os.Args[1:]))
	}
	if err := os.Setenv("CODEDOC_EMBEDDER", "off"); err != nil {
		panic(err)
	}
	// Private home, state, data, config and runtime directories keep the
	// tests out of the user's files and away from a daemon the user runs.
	os.Exit(testutil.MainIsolated(m))
}

func TestIndexReportsVectors(t *testing.T) {
	t.Setenv("CODEDOC_EMBEDDER", "fake")
	setupWorkspace(t)
	out, err := runCLI(t, "index", "--workspace", "fx", "--verbose")
	if err != nil || !strings.Contains(out, "vectors  +") {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err = runCLI(t, "status", "--workspace", "fx", "--repo", "go-lib")
	if err != nil || !strings.Contains(out, " vectors:18/18 ") {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err = runCLI(t, "index", "--workspace", "fx", "--verbose")
	if err != nil || !strings.Contains(out, "vectors  +0 symbols +0 modules") {
		t.Fatalf("%v\n%s", err, out)
	}
	t.Setenv("CODEDOC_EMBEDDER", "")
	t.Setenv("CODEDOC_MODEL_DIR", t.TempDir())
	out, err = runCLI(t, "index", "--workspace", "fx", "--verbose")
	if err != nil || !strings.Contains(out, "vectors  embedding model unavailable") ||
		!strings.Contains(out, "embeddings disabled") {
		t.Fatalf("%v\n%s", err, out)
	}
}

// The index section counts the repos: the line about the vectors is a
// section of its own, in text without a title.
func TestIndexDoesNotCountTheVectorsLineAsARepo(t *testing.T) {
	t.Setenv("CODEDOC_EMBEDDER", "fake")
	setupWorkspace(t)
	out, err := runCLI(t, "index", "--workspace", "fx", "--verbose")
	if err != nil || !strings.Contains(out, "\nvectors  +") {
		t.Fatalf("%v\n%s", err, out)
	}
	repos := strings.Count(out[strings.Index(out, "== index ("):strings.Index(out, "\nvectors  +")], "\n")
	if !strings.Contains(out, fmt.Sprintf("== index (%d)\n", repos)) || repos != 2 {
		t.Fatalf("%d repos listed:\n%s", repos, out)
	}
	out, err = runCLI(t, "index", "--workspace", "fx", "--verbose", "--format", "json")
	if err != nil || !strings.Contains(out, `{"title":"vectors","entries":[{"key":"vectors","text":"+0 symbols +0 modules"`) {
		t.Fatalf("%v\n%s", err, out)
	}
}

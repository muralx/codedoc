//go:build integration

package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestIntegrationSemanticSearch indexes the fixture workspace with the
// real nomic-embed-text-v1.5 model and checks the relevance of search:
// prose queries sharing almost no words with what they mean find it
// among the first three hits, and each hit names the lines that matched. It
// fails when CODEDOC_MODEL_DIR is unset: the Docker integration image
// provides the model in /models.
func TestIntegrationSemanticSearch(t *testing.T) {
	if os.Getenv("CODEDOC_MODEL_DIR") == "" {
		t.Fatal("CODEDOC_MODEL_DIR is not set; run make download-model or use make docker-integration")
	}
	t.Setenv("CODEDOC_EMBEDDER", "")
	e2eWorkspace(t)
	out, err := runCLI(t, "status", "--workspace", "fx", "--repo", "go-lib")
	if err != nil || !strings.Contains(out, " vectors:18/18 ") {
		t.Fatalf("%v\n%s", err, out)
	}
	for query, want := range map[string]string{
		"error when nothing is stored under a name":   "go-lib:go:github.com/fixture/lib/store:ErrMissing",
		"environment variable for the operating mode": "go-lib:go:github.com/fixture/lib/service:Mode",
	} {
		checkSearchTop(t, query, want)
	}
}

// checkSearchTop checks that a search of the Go code of the fixture
// workspace ranks the symbol of key want among its first three hits,
// with the passage that matched, and counts no more than fifty hits.
func checkSearchTop(t *testing.T, query, want string) {
	t.Helper()
	out, err := runCLI(t, "search", query, "--workspace", "fx", "--lang", "go", "--limit", "3", "--format", "json")
	if err != nil {
		t.Fatalf("%q: %v\n%s", query, err, out)
	}
	var got struct {
		Note     string `json:"note"`
		Sections []struct {
			Entries []struct {
				Key   string `json:"key"`
				Extra struct {
					Passages []struct {
						Start, End int
						Score      float64
					} `json:"passages"`
				} `json:"extra"`
			} `json:"entries"`
			Omitted int `json:"omitted"`
		} `json:"sections"`
	}
	if err := json.Unmarshal([]byte(out[strings.Index(out, "{"):]), &got); err != nil || len(got.Sections) != 1 {
		t.Fatalf("%q: %v\n%s", query, err, out)
	}
	if strings.Contains(got.Note, "text search only") || got.Sections[0].Omitted > 50 {
		t.Fatalf("%q: note %q, %d more hits\n%s", query, got.Note, got.Sections[0].Omitted, out)
	}
	for _, e := range got.Sections[0].Entries {
		if e.Key == want {
			if p := e.Extra.Passages; len(p) == 0 || p[0].Start < 1 || p[0].End < p[0].Start || p[0].Score <= 0 {
				t.Fatalf("%q: %s has no matching passage: %+v", query, want, p)
			}
			return
		}
	}
	t.Fatalf("%q: %s is not among the first three\n%s", query, want, out)
}

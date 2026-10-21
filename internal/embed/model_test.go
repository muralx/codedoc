package embed

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// modelEmbedder returns the real nomic embedder when CODEDOC_MODEL_DIR holds
// the model and tokenizer, and skips the test otherwise.
func modelEmbedder(t *testing.T) Embedder {
	t.Helper()
	dir := os.Getenv("CODEDOC_MODEL_DIR")
	if dir == "" {
		t.Skip("CODEDOC_MODEL_DIR not set")
	}
	model, tok := filepath.Join(dir, ModelFile), filepath.Join(dir, TokenizerFile)
	for _, p := range []string{model, tok} {
		if _, err := os.Stat(p); err != nil {
			t.Skipf("model file missing: %v", err)
		}
	}
	e, err := NewEmbedderWithOptions(model, tok, WithMaxLength(128))
	if err != nil {
		t.Fatalf("NewEmbedder: %v", err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e
}

func TestModelDimensionsAndSimilarity(t *testing.T) {
	e := modelEmbedder(t)
	docs, err := Documents(e, []string{
		"Open a pooled connection to the SQL database.",
		"Acquire a connection from the database connection pool.",
		"Render a markdown heading as HTML.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if e.Dimensions() != 768 || len(docs[0]) != 768 {
		t.Fatalf("dims = %d/%d, want 768", e.Dimensions(), len(docs[0]))
	}
	near, far := cosine(docs[0], docs[1]), cosine(docs[0], docs[2])
	t.Logf("similar=%.4f unrelated=%.4f", near, far)
	if near <= far {
		t.Fatalf("similar %v <= unrelated %v", near, far)
	}
}

func TestModelQueryPrefix(t *testing.T) {
	e := modelEmbedder(t)
	q, err := Query(e, "how do I get a database connection")
	if err != nil {
		t.Fatal(err)
	}
	docs, err := Documents(e, []string{
		"Acquire a connection from the database connection pool.",
		"Render a markdown heading as HTML.",
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := mustEmbed(t, e, "how do I get a database connection")
	t.Logf("query->relevant=%.4f query->irrelevant=%.4f query vs unprefixed=%.4f",
		cosine(q, docs[0]), cosine(q, docs[1]), cosine(q, raw))
	if cosine(q, docs[0]) <= cosine(q, docs[1]) {
		t.Fatal("query closer to the irrelevant document")
	}
	if cosine(q, raw) > 0.9999 {
		t.Fatal("query prefix had no effect on the vector")
	}
}

// TestModelPaddingToLongestMatchesFullPadding checks that padding each
// batch only to its longest sequence leaves the vectors as padding to
// maxLength did.
func TestModelPaddingToLongestMatchesFullPadding(t *testing.T) {
	e, ok := modelEmbedder(t).(*ONNXEmbedder)
	if !ok {
		t.Fatal("not an ONNX embedder")
	}
	texts := []string{
		"Open a pooled connection to the SQL database.",
		"x",
		strings.Repeat("Render a markdown heading as HTML and escape its text. ", 20),
		"func (m *MemStore) Get(key string) (string, error)\nGet returns the value stored under key.",
		"store/store.go\nMemStore\nPut implements Store.",
	}
	short, err := e.EmbedBatch(texts)
	if err != nil {
		t.Fatal(err)
	}
	full, err := e.embedPadded(texts, e.cfg.maxLength)
	if err != nil {
		t.Fatal(err)
	}
	worst := 1.0
	for i := range texts {
		worst = min(worst, cosine(short[i], full[i]))
	}
	t.Logf("least cosine to the fully padded vectors: %.7f", worst)
	if worst < 1-1e-4 {
		t.Fatalf("cosine %v to the fully padded vector", worst)
	}
}

// TestModelReloadsAfterTheLastClose closes the only embedder, which
// destroys the ONNX environment, and loads the model again.
func TestModelReloadsAfterTheLastClose(t *testing.T) {
	first := modelEmbedder(t)
	want, err := first.Embed("reload the model")
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	runtimeState.mu.Lock()
	initialized := runtimeState.initialized
	runtimeState.mu.Unlock()
	if initialized {
		t.Fatal("environment still initialized after the last close")
	}
	got, err := modelEmbedder(t).Embed("reload the model")
	if err != nil {
		t.Fatal(err)
	}
	for i := range want {
		if d := want[i] - got[i]; d > 1e-5 || d < -1e-5 {
			t.Fatalf("vector differs at %d: %v vs %v", i, want[i], got[i])
		}
	}
}

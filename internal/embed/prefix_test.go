package embed

import (
	"reflect"
	"testing"
)

// recorder is an Embedder that records the texts it receives.
type recorder struct {
	Fake
	seen []string
}

func (r *recorder) Embed(text string) ([]float32, error) {
	r.seen = append(r.seen, text)
	return r.Fake.Embed(text)
}

func (r *recorder) EmbedBatch(texts []string) ([][]float32, error) {
	r.seen = append(r.seen, texts...)
	return r.Fake.EmbedBatch(texts)
}

func TestPrefixHelpers(t *testing.T) {
	r := &recorder{Fake: *NewFake(8)}
	_, _ = Document(r, "a")
	_, _ = Query(r, "b")
	in := []string{"c", "d"}
	_, _ = Documents(r, in)
	want := []string{"search_document: a", "search_query: b", "search_document: c", "search_document: d"}
	if !reflect.DeepEqual(r.seen, want) {
		t.Fatalf("seen = %q, want %q", r.seen, want)
	}
	if in[0] != "c" {
		t.Fatal("Documents mutated its input")
	}
}

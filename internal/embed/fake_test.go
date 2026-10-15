package embed

import (
	"math"
	"testing"
)

func cosine(a, b []float32) float64 {
	var dot float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
	}
	return dot
}

func norm(v []float32) float64 {
	return math.Sqrt(cosine(v, v))
}

func mustEmbed(t *testing.T, e Embedder, text string) []float32 {
	t.Helper()
	v, err := e.Embed(text)
	if err != nil {
		t.Fatalf("Embed(%q): %v", text, err)
	}
	return v
}

func TestFakeDeterministicAndNormalized(t *testing.T) {
	f := NewFake(64)
	a := mustEmbed(t, f, "Parse the Config file")
	b := mustEmbed(t, NewFake(64), "parse the config FILE")
	if len(a) != 64 || f.Dimensions() != 64 {
		t.Fatalf("dims = %d/%d, want 64", len(a), f.Dimensions())
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("vectors differ at %d: case or instance changed the output", i)
		}
	}
	if n := norm(a); math.Abs(n-1) > 1e-6 {
		t.Fatalf("norm = %v, want 1", n)
	}
}

func TestFakeSimilarity(t *testing.T) {
	f := NewFake(256)
	base := mustEmbed(t, f, "open the database connection pool")
	near := mustEmbed(t, f, "close the database connection")
	far := mustEmbed(t, f, "render markdown headings")
	if cosine(base, near) <= cosine(base, far) {
		t.Fatalf("similar %v <= unrelated %v", cosine(base, near), cosine(base, far))
	}
}

func TestFakeEdgeCases(t *testing.T) {
	f := NewFake(0)
	if f.Dimensions() != 64 {
		t.Fatalf("default dims = %d, want 64", f.Dimensions())
	}
	if n := norm(mustEmbed(t, f, " ,;: ")); n != 0 {
		t.Fatalf("no-word text norm = %v, want 0", n)
	}
	q, _ := Query(f, "load config")
	d, _ := Document(f, "load config")
	if cosine(q, d) < 0.999999 {
		t.Fatalf("fake must ignore task prefixes, cosine = %v", cosine(q, d))
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestFakeBatch(t *testing.T) {
	f := NewFake(32)
	if out, err := f.EmbedBatch(nil); out != nil || err != nil {
		t.Fatalf("empty batch = %v, %v", out, err)
	}
	out, err := f.EmbedBatch([]string{"alpha", "beta"})
	if err != nil || len(out) != 2 {
		t.Fatalf("batch = %d, %v", len(out), err)
	}
	if cosine(out[1], mustEmbed(t, f, "beta")) < 0.999999 {
		t.Fatal("batch output out of order")
	}
}

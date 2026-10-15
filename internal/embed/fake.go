package embed

import (
	"strings"
	"unicode"

	"github.com/cespare/xxhash/v2"
)

// Fake is a deterministic Embedder for tests. It hashes lower-cased word
// tokens into a bag-of-words vector of the configured size and L2-normalizes
// it, so texts that share words have a higher cosine similarity. The nomic
// task prefixes are ignored, so Document and Query of the same text
// agree. It needs no model or native library and is safe for concurrent use.
type Fake struct {
	dims int
}

// NewFake returns a Fake producing vectors of dims dimensions. A
// non-positive dims is replaced by 64.
func NewFake(dims int) *Fake {
	if dims <= 0 {
		dims = 64
	}
	return &Fake{dims: dims}
}

// Embed returns the normalized bag-of-words vector of text. Text without
// word tokens yields the zero vector.
func (f *Fake) Embed(text string) ([]float32, error) {
	vec := make([]float32, f.dims)
	text = strings.TrimPrefix(strings.TrimPrefix(text, DocumentPrefix), QueryPrefix)
	for _, w := range words(text) {
		vec[xxhash.Sum64String(w)%uint64(f.dims)]++
	}
	l2Normalize(vec)
	return vec, nil
}

// EmbedBatch embeds each text in order.
func (f *Fake) EmbedBatch(texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i], _ = f.Embed(t)
	}
	return out, nil
}

// Dimensions returns the vector size.
func (f *Fake) Dimensions() int {
	return f.dims
}

// Close is a no-op.
func (f *Fake) Close() error {
	return nil
}

// words splits text into lower-cased runs of letters and digits.
func words(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

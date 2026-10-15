package embed

// nomic-embed-text-v1.5 is trained with task prefixes: stored passages are
// embedded with DocumentPrefix and search queries with QueryPrefix. Mixing
// them up, or omitting them, degrades retrieval quality.
const (
	DocumentPrefix = "search_document: "
	QueryPrefix    = "search_query: "
)

// Document embeds text as a stored document (with DocumentPrefix).
func Document(e Embedder, text string) ([]float32, error) {
	return e.Embed(DocumentPrefix + text)
}

// Query embeds text as a search query (with QueryPrefix).
func Query(e Embedder, text string) ([]float32, error) {
	return e.Embed(QueryPrefix + text)
}

// Documents embeds texts as stored documents, in input order.
func Documents(e Embedder, texts []string) ([][]float32, error) {
	return e.EmbedBatch(withPrefix(DocumentPrefix, texts))
}

// withPrefix returns a copy of texts with prefix prepended to each element.
func withPrefix(prefix string, texts []string) []string {
	out := make([]string, len(texts))
	for i, t := range texts {
		out[i] = prefix + t
	}
	return out
}

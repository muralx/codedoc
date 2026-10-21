// Package embed turns text into dense vectors for semantic search. It ships
// an ONNX Runtime embedder for the nomic-embed-text-v1.5 model, a
// deterministic hashing embedder for tests, and helpers that compose the
// text embedded for symbols and modules.
package embed

import "errors"

// Embedder produces vector embeddings from text inputs.
type Embedder interface {
	// Embed produces a vector embedding for a single text input.
	Embed(text string) ([]float32, error)

	// EmbedBatch produces embeddings for multiple texts, in input order.
	EmbedBatch(texts []string) ([][]float32, error)

	// Dimensions returns the embedding vector size.
	Dimensions() int

	// Close releases resources.
	Close() error
}

// ModelFile and TokenizerFile are the file names expected inside a model
// directory.
const (
	ModelFile     = "nomic-embed-text-v1.5.onnx"
	TokenizerFile = "tokenizer.json"
)

// NewEmbedder creates an Embedder backed by ONNX Runtime inference.
// modelPath is the path to the ONNX model file and tokenizerPath the path
// to a HuggingFace tokenizer.json file.
func NewEmbedder(modelPath, tokenizerPath string) (Embedder, error) {
	return NewONNXEmbedder(modelPath, tokenizerPath)
}

// NewEmbedderWithOptions creates an ONNX-backed Embedder with custom
// configuration.
func NewEmbedderWithOptions(modelPath, tokenizerPath string, opts ...Option) (Embedder, error) {
	return NewONNXEmbedderWithOptions(modelPath, tokenizerPath, opts...)
}

// DefaultBatchSize is the texts of one inference call. Attention memory
// grows with the batch times the square of its longest text: embedding
// 178 passages of 1,200 bytes peaked at 5.1 GB with a batch of 32 and
// the arena on, 2.2 GB with a batch of 8 and the arena off, in the same
// 23 seconds; 4 saves nothing more.
const DefaultBatchSize = 8

// Option configures an ONNXEmbedder.
type Option func(*onnxConfig)

type onnxConfig struct {
	maxLength   int
	batchSize   int
	dims        int
	threads     int
	libraryPath string
	// arena keeps ONNX Runtime's CPU memory arena and memory patterns:
	// faster repeated runs, but the peak of the largest batch stays
	// allocated for the life of the session.
	arena bool
}

func defaultConfig() onnxConfig {
	return onnxConfig{
		maxLength:   512,
		batchSize:   DefaultBatchSize,
		dims:        768,
		threads:     DefaultThreads(),
		libraryPath: LibraryPath(),
	}
}

// WithMaxLength sets the maximum token sequence length (default 512; the
// model supports up to 8192).
func WithMaxLength(n int) Option {
	return func(c *onnxConfig) {
		if n > 0 {
			c.maxLength = n
		}
	}
}

// WithBatchSize sets the number of texts processed per ONNX inference call
// (default DefaultBatchSize).
func WithBatchSize(n int) Option {
	return func(c *onnxConfig) {
		if n > 0 {
			c.batchSize = n
		}
	}
}

// WithDimensions overrides the expected embedding dimensions (default 768).
func WithDimensions(n int) Option {
	return func(c *onnxConfig) {
		if n > 0 {
			c.dims = n
		}
	}
}

// WithLibraryPath overrides the ONNX Runtime shared library path (default
// from LibraryPath). The runtime is process-wide: the path only takes effect
// for the first embedder that initializes it successfully.
func WithLibraryPath(path string) Option {
	return func(c *onnxConfig) {
		if path != "" {
			c.libraryPath = path
		}
	}
}

// ErrONNXRuntimeNotAvailable is returned when the ONNX Runtime shared library
// cannot be loaded, typically because libonnxruntime.so is not installed or
// CODEDOC_ONNXRUNTIME_LIB points at the wrong file.
var ErrONNXRuntimeNotAvailable = errors.New("ONNX Runtime shared library not available; install libonnxruntime or set " + LibraryEnv)

// ErrClosed is returned by an embedder used after Close.
var ErrClosed = errors.New("embedder is closed")

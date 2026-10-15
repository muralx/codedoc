package embed

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// ErrUnavailable is returned by a Lazy embedder whose model could not be
// loaded; callers fall back to text search.
var ErrUnavailable = errors.New("embedding model unavailable")

// ModelDims is the vector size of nomic-embed-text-v1.5.
const ModelDims = 768

// FakeDims is the vector size of the fake embedder chosen with
// EmbedderEnv=fake.
const FakeDims = 64

// EmbedderEnv names the environment variable that selects the embedder:
// "off" disables embeddings, anything else uses the configured model.
// "fake" is a test and debug switch, not a user setting: it selects the
// deterministic hashing embedder, whose vectors carry no meaning, so
// end-to-end tests and debugging runs exercise the vector paths without
// a model.
const EmbedderEnv = "CODEDOC_EMBEDDER"

// Lazy is an Embedder that loads its backing embedder on first use, so a
// process that never embeds never pays for loading the model. A failed
// load is reported once to the warning writer; every call then returns
// ErrUnavailable. It is safe for concurrent use.
type Lazy struct {
	dims int
	open func() (Embedder, error)
	warn io.Writer

	mu    sync.Mutex
	tried bool
	e     Embedder
	err   error
}

// NewLazy returns a Lazy embedder of dims dimensions that loads with open
// and reports a failed load to warn (nil discards it).
func NewLazy(dims int, open func() (Embedder, error), warn io.Writer) *Lazy {
	return &Lazy{dims: dims, open: open, warn: warn}
}

func (l *Lazy) load() (Embedder, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.tried {
		return l.e, l.err
	}
	l.tried = true
	l.e, l.err = l.open()
	if l.err != nil {
		l.e = nil
		if l.warn != nil {
			_, _ = fmt.Fprintf(l.warn, "codedoc: embeddings disabled, search is text-only: %v\n", l.err)
		}
		l.err = fmt.Errorf("%w: %w", ErrUnavailable, l.err)
	}
	return l.e, l.err
}

// Embed loads the model if needed and embeds text.
func (l *Lazy) Embed(text string) ([]float32, error) {
	e, err := l.load()
	if err != nil {
		return nil, err
	}
	return e.Embed(text)
}

// EmbedBatch loads the model if needed and embeds texts in order.
func (l *Lazy) EmbedBatch(texts []string) ([][]float32, error) {
	e, err := l.load()
	if err != nil {
		return nil, err
	}
	return e.EmbedBatch(texts)
}

// Dimensions returns the configured vector size without loading.
func (l *Lazy) Dimensions() int { return l.dims }

// Loaded reports whether the backing embedder is loaded and usable.
func (l *Lazy) Loaded() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.e != nil
}

// Close releases the backing embedder when it was loaded.
func (l *Lazy) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.e != nil {
		return l.e.Close()
	}
	return nil
}

// Warm reports whether e embeds without first loading a model: true for
// every embedder except a Lazy one not loaded yet.
func Warm(e Embedder) bool {
	l, ok := e.(*Lazy)
	return !ok || l.Loaded()
}

// Configured returns the embedder for a model directory: nil when
// EmbedderEnv is "off" or modelDir is empty, a Fake when EmbedderEnv is
// "fake", else a Lazy ONNX embedder over the directory's model and
// tokenizer that reports a failed load to warn.
func Configured(modelDir string, warn io.Writer) Embedder {
	switch os.Getenv(EmbedderEnv) {
	case "fake":
		return NewFake(FakeDims)
	case "off":
		return nil
	}
	if modelDir == "" {
		return nil
	}
	return NewLazy(ModelDims, func() (Embedder, error) {
		model, tok := filepath.Join(modelDir, ModelFile), filepath.Join(modelDir, TokenizerFile)
		for _, p := range []string{model, tok} {
			if _, err := os.Stat(p); err != nil {
				return nil, err
			}
		}
		return NewEmbedder(model, tok)
	}, warn)
}

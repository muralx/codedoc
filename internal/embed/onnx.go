package embed

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

// ONNXEmbedder implements Embedder using ONNX Runtime for inference over a
// BERT-style model with mean pooling and L2 normalization. It is safe for
// concurrent use; inference calls are serialized.
type ONNXEmbedder struct {
	mu        sync.Mutex
	session   *ort.DynamicAdvancedSession
	tokenizer *wordPieceTokenizer
	cfg       onnxConfig
	closed    bool
}

// NewONNXEmbedder creates an ONNXEmbedder with default settings.
func NewONNXEmbedder(modelPath, tokenizerPath string) (*ONNXEmbedder, error) {
	return NewONNXEmbedderWithOptions(modelPath, tokenizerPath)
}

// NewONNXEmbedderWithOptions creates an ONNXEmbedder with custom options. It
// loads the ONNX Runtime shared library on first use and returns an error
// wrapping ErrONNXRuntimeNotAvailable when that fails.
func NewONNXEmbedderWithOptions(modelPath, tokenizerPath string, opts ...Option) (*ONNXEmbedder, error) {
	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}
	tok, err := newWordPieceTokenizer(tokenizerPath, cfg.maxLength)
	if err != nil {
		return nil, fmt.Errorf("load tokenizer: %w", err)
	}
	if err := initRuntime(cfg.libraryPath); err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrONNXRuntimeNotAvailable, cfg.libraryPath, err)
	}
	so, err := sessionOptions(cfg.threads)
	if err != nil {
		return nil, fmt.Errorf("ONNX session options: %w", err)
	}
	defer func() { _ = so.Destroy() }()
	session, err := ort.NewDynamicAdvancedSession(modelPath,
		[]string{"input_ids", "attention_mask", "token_type_ids"},
		[]string{"last_hidden_state"}, so)
	if err != nil {
		return nil, fmt.Errorf("create ONNX session: %w", err)
	}
	return &ONNXEmbedder{session: session, tokenizer: tok, cfg: cfg}, nil
}

// Embed produces a vector embedding for a single text input.
func (e *ONNXEmbedder) Embed(text string) ([]float32, error) {
	results, err := e.EmbedBatch([]string{text})
	if err != nil {
		return nil, err
	}
	return results[0], nil
}

// EmbedBatch produces embeddings for multiple texts, in input order. The
// texts are tokenized first and run through the model shortest first, in
// chunks of the configured batch size, each padded only to its longest
// sequence: the attention mask keeps padding out of the result, so short
// texts no longer pay for maxLength positions.
func (e *ONNXEmbedder) EmbedBatch(texts []string) ([][]float32, error) {
	return e.embedPadded(texts, 0)
}

// embedPadded is EmbedBatch padding every chunk to at least seqLen
// positions (0: to its longest sequence).
func (e *ONNXEmbedder) embedPadded(texts []string, seqLen int) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, ErrClosed
	}
	seqs := make([][]int32, len(texts))
	for i, text := range texts {
		seqs[i] = e.tokenizer.encode(text)
	}
	order := lengthOrder(seqs)
	all := make([][]float32, len(texts))
	for start := 0; start < len(order); start += e.cfg.batchSize {
		idx := order[start:min(start+e.cfg.batchSize, len(order))]
		results, err := e.embedBatch(e.tokenizeBatch(seqs, idx, seqLen))
		if err != nil {
			return nil, fmt.Errorf("batch starting at index %d: %w", start, err)
		}
		for j, i := range idx {
			all[i] = results[j]
		}
	}
	return all, nil
}

// lengthOrder returns the indexes of seqs ordered by sequence length,
// shortest first; equal lengths keep their input order.
func lengthOrder(seqs [][]int32) []int {
	order := make([]int, len(seqs))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return len(seqs[order[a]]) < len(seqs[order[b]]) })
	return order
}

// Dimensions returns the embedding vector size.
func (e *ONNXEmbedder) Dimensions() int {
	return e.cfg.dims
}

// Close releases the ONNX session. Closing twice is a no-op.
func (e *ONNXEmbedder) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil
	}
	e.closed = true
	if e.session != nil {
		if err := e.session.Destroy(); err != nil {
			return fmt.Errorf("destroy ONNX session: %w", err)
		}
	}
	return nil
}

// batchInput is the tokenized, flattened form of one inference batch.
type batchInput struct {
	size, seqLen          int
	ids, mask, tokenTypes []int64
}

// tokenizeBatch lays out the sequences seqs[idx] as flat [size*seqLen]
// input arrays, seqLen being the longest of them or minLen when longer.
func (e *ONNXEmbedder) tokenizeBatch(seqs [][]int32, idx []int, minLen int) batchInput {
	in := batchInput{size: len(idx), seqLen: max(minLen, 1)}
	for _, i := range idx {
		in.seqLen = max(in.seqLen, len(seqs[i]))
	}
	n := in.size * in.seqLen
	in.ids = make([]int64, 0, n)
	in.mask = make([]int64, 0, n)
	in.tokenTypes = make([]int64, 0, n)
	for _, i := range idx {
		tr := e.tokenizer.pad(seqs[i], in.seqLen)
		in.ids = append(in.ids, tr.InputIDs...)
		in.mask = append(in.mask, tr.AttentionMask...)
		in.tokenTypes = append(in.tokenTypes, tr.TokenTypeIDs...)
	}
	return in
}

// embedBatch runs inference on one laid out batch; the caller holds mu.
func (e *ONNXEmbedder) embedBatch(in batchInput) ([][]float32, error) {
	out, err := e.run(in)
	if err != nil {
		return nil, err
	}
	defer func() { _ = out.Destroy() }()
	return meanPool(out.GetData(), out.GetShape(), in), nil
}

// run executes the session and returns the last_hidden_state tensor, which
// the caller must destroy.
func (e *ONNXEmbedder) run(in batchInput) (*ort.Tensor[float32], error) {
	shape := ort.NewShape(int64(in.size), int64(in.seqLen))
	inputs := make([]ort.ArbitraryTensor, 0, 3)
	defer func() {
		for _, v := range inputs {
			_ = v.Destroy()
		}
	}()
	for _, data := range [][]int64{in.ids, in.mask, in.tokenTypes} {
		t, err := ort.NewTensor(shape, data)
		if err != nil {
			return nil, fmt.Errorf("create input tensor: %w", err)
		}
		inputs = append(inputs, t)
	}
	outputs := []ort.ArbitraryTensor{nil}
	if err := e.session.Run(inputs, outputs); err != nil {
		return nil, fmt.Errorf("ONNX inference: %w", err)
	}
	t, ok := outputs[0].(*ort.Tensor[float32])
	if !ok {
		if outputs[0] != nil {
			_ = outputs[0].Destroy()
		}
		return nil, errors.New("ONNX inference: expected a float32 output tensor")
	}
	return t, nil
}

// meanPool reduces a [batch, seq, hidden] output to [batch, hidden] by
// averaging the hidden states of non-padding tokens, then L2-normalizes each
// row.
func meanPool(data []float32, shape ort.Shape, in batchInput) [][]float32 {
	outSeq, hidden := int(shape[1]), int(shape[len(shape)-1])
	results := make([][]float32, in.size)
	for b := range in.size {
		vec := make([]float32, hidden)
		var count float32
		for s := range min(outSeq, in.seqLen) {
			if in.mask[b*in.seqLen+s] != 1 {
				continue
			}
			count++
			row := data[(b*outSeq+s)*hidden : (b*outSeq+s+1)*hidden]
			for d, x := range row {
				vec[d] += x
			}
		}
		if count > 0 {
			for d := range vec {
				vec[d] /= count
			}
		}
		l2Normalize(vec)
		results[b] = vec
	}
	return results
}

// l2Normalize scales v in place to unit length; a zero vector is left as is.
func l2Normalize(v []float32) {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return
	}
	norm := float32(math.Sqrt(sum))
	for i := range v {
		v[i] /= norm
	}
}

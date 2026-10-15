package embed

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"
)

// wordPieceTokenizer is a simplified WordPiece tokenizer that reads a
// HuggingFace tokenizer.json to build a vocabulary, then performs basic
// lower-casing BERT-style tokenization.
type wordPieceTokenizer struct {
	vocab map[string]int32

	clsID int32
	sepID int32
	padID int32
	unkID int32

	maxLength int
}

// huggingFaceTokenizerJSON is the subset of the HuggingFace tokenizer.json
// format the tokenizer needs.
type huggingFaceTokenizerJSON struct {
	Model struct {
		Type  string           `json:"type"`
		Vocab map[string]int32 `json:"vocab"`
	} `json:"model"`
	AddedTokens []struct {
		ID      int32  `json:"id"`
		Content string `json:"content"`
		Special bool   `json:"special"`
	} `json:"added_tokens"`
}

// newWordPieceTokenizer loads a HuggingFace tokenizer.json file. Sequences
// are padded or truncated to maxLength tokens.
func newWordPieceTokenizer(path string, maxLength int) (*wordPieceTokenizer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read tokenizer file: %w", err)
	}
	var tf huggingFaceTokenizerJSON
	if err := json.Unmarshal(data, &tf); err != nil {
		return nil, fmt.Errorf("parse tokenizer JSON: %w", err)
	}
	vocab := make(map[string]int32, len(tf.Model.Vocab)+len(tf.AddedTokens))
	for tok, id := range tf.Model.Vocab {
		vocab[tok] = id
	}
	for _, at := range tf.AddedTokens {
		vocab[at.Content] = at.ID
	}
	if len(vocab) == 0 {
		return nil, fmt.Errorf("tokenizer vocabulary is empty; ensure %s is a valid HuggingFace tokenizer.json", path)
	}
	t := &wordPieceTokenizer{
		vocab:     vocab,
		clsID:     lookupID(vocab, "[CLS]"),
		sepID:     lookupID(vocab, "[SEP]"),
		padID:     lookupID(vocab, "[PAD]"),
		unkID:     lookupID(vocab, "[UNK]"),
		maxLength: maxLength,
	}
	if t.unkID == -1 {
		return nil, errors.New("tokenizer vocabulary missing [UNK] token")
	}
	return t, nil
}

// lookupID returns the id of tok in vocab, or -1 when absent.
func lookupID(vocab map[string]int32, tok string) int32 {
	if id, ok := vocab[tok]; ok {
		return id
	}
	return -1
}

// tokenizeResult holds the integer-encoded output of tokenization.
type tokenizeResult struct {
	InputIDs      []int64
	AttentionMask []int64
	TokenTypeIDs  []int64
}

// Tokenize converts text into model inputs padded or truncated to the
// tokenizer's maxLength.
func (t *wordPieceTokenizer) Tokenize(text string) tokenizeResult {
	return t.pad(t.encode(text), t.maxLength)
}

// pad lays out tokens as model inputs of seqLen positions (at least
// len(tokens)): the padding positions hold the [PAD] id and a zero
// attention mask.
func (t *wordPieceTokenizer) pad(tokens []int32, seqLen int) tokenizeResult {
	seqLen = max(seqLen, len(tokens))
	res := tokenizeResult{
		InputIDs:      make([]int64, seqLen),
		AttentionMask: make([]int64, seqLen),
		TokenTypeIDs:  make([]int64, seqLen),
	}
	for i, id := range tokens {
		res.InputIDs[i] = int64(id)
		res.AttentionMask[i] = 1
	}
	if t.padID != -1 {
		for i := len(tokens); i < seqLen; i++ {
			res.InputIDs[i] = int64(t.padID)
		}
	}
	return res
}

// encode returns the token ids for text wrapped in [CLS] and [SEP] (when the
// vocabulary has them), truncated so the whole sequence fits maxLength.
func (t *wordPieceTokenizer) encode(text string) []int32 {
	budget := t.maxLength
	if t.clsID != -1 {
		budget--
	}
	if t.sepID != -1 {
		budget--
	}
	var content []int32
	for _, word := range preTokenize(strings.ToLower(text)) {
		content = append(content, t.wordPieceEncode(word)...)
		if len(content) >= budget {
			content = content[:max(budget, 0)]
			break
		}
	}
	tokens := make([]int32, 0, len(content)+2)
	if t.clsID != -1 {
		tokens = append(tokens, t.clsID)
	}
	tokens = append(tokens, content...)
	if t.sepID != -1 {
		tokens = append(tokens, t.sepID)
	}
	return tokens
}

// wordPieceEncode breaks a single word into WordPiece sub-tokens using
// greedy longest-prefix matching; an unmatched remainder becomes [UNK].
func (t *wordPieceTokenizer) wordPieceEncode(word string) []int32 {
	if id, ok := t.vocab[word]; ok {
		return []int32{id}
	}
	var tokens []int32
	remaining := word
	for remaining != "" {
		id, n := t.longestPrefix(remaining, len(tokens) > 0)
		if n == 0 {
			return append(tokens, t.unkID)
		}
		tokens = append(tokens, id)
		remaining = remaining[n:]
	}
	return tokens
}

// longestPrefix finds the longest vocabulary entry that prefixes s, trying
// only rune boundaries. Continuation pieces carry the "##" marker. It returns
// the entry's id and byte length, or a zero length when nothing matches.
func (t *wordPieceTokenizer) longestPrefix(s string, continuation bool) (int32, int) {
	for end := len(s); end > 0; end-- {
		if end < len(s) && !utf8.RuneStart(s[end]) {
			continue
		}
		sub := s[:end]
		if continuation {
			sub = "##" + sub
		}
		if id, ok := t.vocab[sub]; ok {
			return id, end
		}
	}
	return 0, 0
}

// preTokenize splits text into words at whitespace; punctuation and symbol
// characters become tokens of their own.
func preTokenize(text string) []string {
	var tokens []string
	var current strings.Builder
	flush := func() {
		if current.Len() > 0 {
			tokens = append(tokens, current.String())
			current.Reset()
		}
	}
	for _, r := range text {
		switch {
		case unicode.IsSpace(r):
			flush()
		case unicode.IsPunct(r) || unicode.IsSymbol(r):
			flush()
			tokens = append(tokens, string(r))
		default:
			current.WriteRune(r)
		}
	}
	flush()
	return tokens
}

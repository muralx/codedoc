package embed

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const tinyTokenizer = `{
  "model": {"type": "WordPiece", "vocab": {
    "[PAD]": 0, "[UNK]": 1, "[CLS]": 2, "[SEP]": 3,
    "hello": 4, "world": 5, "un": 6, "##able": 7, "!": 8, "é": 9, "##é": 10
  }},
  "added_tokens": [{"id": 11, "content": "[MASK]", "special": true}]
}`

func writeFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tokenizer.json")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func tinyTok(t *testing.T, maxLen int) *wordPieceTokenizer {
	t.Helper()
	tok, err := newWordPieceTokenizer(writeFile(t, tinyTokenizer), maxLen)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestTokenizePadsAndMasks(t *testing.T) {
	res := tinyTok(t, 8).Tokenize("Hello, unable world!")
	// [CLS] hello [UNK](",") un ##able world ! [SEP]
	wantIDs := []int64{2, 4, 1, 6, 7, 5, 8, 3}
	if !reflect.DeepEqual(res.InputIDs, wantIDs) {
		t.Fatalf("ids = %v, want %v", res.InputIDs, wantIDs)
	}
	short := tinyTok(t, 6).Tokenize("hello")
	if !reflect.DeepEqual(short.InputIDs, []int64{2, 4, 3, 0, 0, 0}) {
		t.Fatalf("padded ids = %v", short.InputIDs)
	}
	if !reflect.DeepEqual(short.AttentionMask, []int64{1, 1, 1, 0, 0, 0}) {
		t.Fatalf("mask = %v", short.AttentionMask)
	}
	if !reflect.DeepEqual(short.TokenTypeIDs, make([]int64, 6)) {
		t.Fatalf("token types = %v", short.TokenTypeIDs)
	}
}

func TestTokenizeTruncates(t *testing.T) {
	res := tinyTok(t, 4).Tokenize(strings.Repeat("hello ", 10))
	if !reflect.DeepEqual(res.InputIDs, []int64{2, 4, 4, 3}) {
		t.Fatalf("ids = %v", res.InputIDs)
	}
}

func TestWordPieceMultibyte(t *testing.T) {
	tok := tinyTok(t, 8)
	if got := tok.wordPieceEncode("éé"); !reflect.DeepEqual(got, []int32{9, 10}) {
		t.Fatalf("éé = %v", got)
	}
	if got := tok.wordPieceEncode("zzz"); !reflect.DeepEqual(got, []int32{1}) {
		t.Fatalf("zzz = %v", got)
	}
}

func TestTokenizerErrors(t *testing.T) {
	cases := map[string]string{
		"missing file": filepath.Join(t.TempDir(), "nope.json"),
		"bad json":     writeFile(t, "{"),
		"empty vocab":  writeFile(t, `{"model":{"vocab":{}}}`),
		"no unk":       writeFile(t, `{"model":{"vocab":{"a":0}}}`),
	}
	for name, path := range cases {
		if _, err := newWordPieceTokenizer(path, 8); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestTokenizeWithoutSpecialTokens(t *testing.T) {
	tok, err := newWordPieceTokenizer(writeFile(t, `{"model":{"vocab":{"[UNK]":0,"a":1}}}`), 3)
	if err != nil {
		t.Fatal(err)
	}
	res := tok.Tokenize("a a a a")
	if !reflect.DeepEqual(res.InputIDs, []int64{1, 1, 1}) {
		t.Fatalf("ids = %v", res.InputIDs)
	}
}

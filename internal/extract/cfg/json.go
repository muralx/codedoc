package cfg

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
)

// jsonExtractor defines every scalar leaf as the dotted path of its object
// keys, with array items by index. Lines come from decoder offsets.
type jsonExtractor struct{}

// Extract implements Extractor.
func (jsonExtractor) Extract(_ string, src []byte) (*Result, error) {
	w := &jsonWalker{r: &Result{Format: JSON}, lines: newLineIndex(src)}
	w.dec = json.NewDecoder(bytes.NewReader(src))
	w.dec.UseNumber()
	if err := w.value("", 0); err != nil {
		return w.r, err
	}
	if _, err := w.dec.Token(); !errors.Is(err, io.EOF) {
		return w.r, fmt.Errorf("json: data after the top-level value")
	}
	return w.r, nil
}

// jsonWalker walks the token stream of one JSON document.
type jsonWalker struct {
	r     *Result
	dec   *json.Decoder
	lines lineIndex
}

// line returns the line of the token the decoder just returned.
func (w *jsonWalker) line() int {
	return w.lines.line(w.dec.InputOffset() - 1)
}

// value reads one value whose dotted path is key, defined at line (0 means
// the line of the value itself).
func (w *jsonWalker) value(key string, line int) error {
	tok, err := w.dec.Token()
	if err != nil {
		return err
	}
	if line == 0 {
		line = w.line()
	}
	switch tok {
	case json.Delim('{'):
		return w.object(key, line)
	case json.Delim('['):
		return w.array(key, line)
	}
	text := fmt.Sprint(tok)
	if tok == nil {
		text = "null"
	}
	defineLeaf(w.r, key, line, text)
	if s, ok := tok.(string); ok {
		w.r.addPlaceholders(s, w.line())
	}
	return nil
}

// object reads the members of an object after its opening brace.
func (w *jsonWalker) object(prefix string, line int) error {
	empty := true
	for w.dec.More() {
		empty = false
		tok, err := w.dec.Token()
		if err != nil {
			return err
		}
		if err := w.value(join(prefix, tok.(string)), w.line()); err != nil {
			return err
		}
	}
	if empty {
		defineLeaf(w.r, prefix, line, "{}")
	}
	_, err := w.dec.Token()
	return err
}

// array reads the items of an array after its opening bracket.
func (w *jsonWalker) array(prefix string, line int) error {
	i := 0
	for ; w.dec.More(); i++ {
		if err := w.value(join(prefix, strconv.Itoa(i)), 0); err != nil {
			return err
		}
	}
	if i == 0 {
		defineLeaf(w.r, prefix, line, "[]")
	}
	_, err := w.dec.Token()
	return err
}

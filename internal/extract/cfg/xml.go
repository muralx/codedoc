package cfg

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strings"
)

// xmlExtractor defines elements with text as their element path below the
// root (a.b.c), attributes as path@attr, and the children of a Maven
// <project><properties> element also by their bare name.
type xmlExtractor struct{}

// Extract implements Extractor.
func (xmlExtractor) Extract(_ string, src []byte) (*Result, error) {
	w := &xmlWalker{r: &Result{Format: XML}, lines: newLineIndex(src)}
	dec := xml.NewDecoder(bytes.NewReader(src))
	for {
		off := dec.InputOffset()
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return w.r, nil
		}
		if err != nil {
			return w.r, err
		}
		w.token(tok, w.lines.line(off))
	}
}

// xmlElement is an open element: its name, start line and text so far.
type xmlElement struct {
	name string
	line int
	text strings.Builder
}

// xmlWalker tracks the open elements of one document.
type xmlWalker struct {
	r     *Result
	lines lineIndex
	stack []*xmlElement
}

// token handles one token that starts at line.
func (w *xmlWalker) token(tok xml.Token, line int) {
	switch t := tok.(type) {
	case xml.StartElement:
		w.stack = append(w.stack, &xmlElement{name: t.Name.Local, line: line})
		w.attributes(t.Attr, line)
	case xml.CharData:
		w.charData(string(t), line)
	case xml.EndElement:
		w.end()
	}
}

// path returns the element path of the open element, without the root.
func (w *xmlWalker) path() string {
	names := make([]string, 0, len(w.stack))
	for _, e := range w.stack[1:] {
		names = append(names, e.name)
	}
	return strings.Join(names, ".")
}

// attributes defines the attributes of the element just opened, skipping
// the root element and namespaced attributes (xmlns, xsi:...).
func (w *xmlWalker) attributes(attrs []xml.Attr, line int) {
	if len(w.stack) < 2 {
		return
	}
	p := w.path()
	for _, a := range attrs {
		if a.Name.Space != "" || a.Name.Local == "xmlns" {
			continue
		}
		w.r.addDef(p+"@"+a.Name.Local, line, a.Value)
		w.r.addPlaceholders(a.Value, line)
	}
}

// charData appends text to the open element and records its placeholders.
func (w *xmlWalker) charData(text string, line int) {
	w.r.addPlaceholders(text, line)
	if len(w.stack) == 0 {
		return
	}
	w.stack[len(w.stack)-1].text.WriteString(text)
}

// end closes the innermost element, defining it when it holds text.
func (w *xmlWalker) end() {
	top := w.stack[len(w.stack)-1]
	if text := strings.TrimSpace(top.text.String()); text != "" && len(w.stack) > 1 {
		w.r.addDef(w.path(), top.line, text)
		if w.isMavenProperty() {
			w.r.addDef(top.name, top.line, text)
		}
	}
	w.stack = w.stack[:len(w.stack)-1]
}

// isMavenProperty reports whether the innermost element is a child of
// <project><properties>.
func (w *xmlWalker) isMavenProperty() bool {
	return len(w.stack) == 3 && w.stack[0].name == "project" && w.stack[1].name == "properties"
}

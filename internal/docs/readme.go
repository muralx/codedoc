package docs

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/muralx/codedoc/internal/extract/doc"
)

// readme returns the module doc README.md of dir holds: the body of its
// "## Purpose" section (where codedoc doc set writes) up to the next
// heading of level one or two, as the writer reads it, else the text
// under its first heading up to the next one, else its text before any
// heading. A missing README yields no doc. Headings are found as the
// document extractor finds them (doc.MarkdownSections).
func readme(root, dir string) (ModuleDoc, error) {
	p := path.Join(dir, "README.md")
	src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
	if errors.Is(err, fs.ErrNotExist) {
		return ModuleDoc{}, nil
	}
	if err != nil {
		return ModuleDoc{}, err
	}
	secs := doc.MarkdownSections(string(src))
	text := ""
	for i, s := range secs {
		if s.Level == 2 && strings.EqualFold(s.Title, "Purpose") {
			text = purposeBody(secs[i:])
			break
		}
	}
	if text == "" && len(secs) > 1 {
		text = secs[1].Body
	}
	if text == "" {
		text = secs[0].Body
	}
	if text = strings.TrimSpace(text); text == "" {
		return ModuleDoc{}, nil
	}
	return ModuleDoc{Text: text, Source: p}, nil
}

// purposeBody joins the body of the first section with the subsections
// that follow it until a heading of level one or two.
func purposeBody(secs []doc.Section) string {
	var b strings.Builder
	b.WriteString(secs[0].Body)
	for _, s := range secs[1:] {
		if s.Level <= 2 {
			break
		}
		b.WriteString(s.Heading + "\n" + s.Body)
	}
	return b.String()
}

// Headings returns the headings of Markdown text outside code fences
// whose level is at most maxLevel, in order, each as "## Title".
func Headings(src string, maxLevel int) []string {
	var out []string
	for _, s := range doc.MarkdownSections(src)[1:] {
		if s.Level <= maxLevel {
			out = append(out, s.Heading)
		}
	}
	return out
}

package write

import (
	"fmt"
	"slices"
	"strings"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/parse"
)

// javaLang writes Javadoc comments and package-info.java.
type javaLang struct{}

// javaKeptTags are the block tags an existing Javadoc keeps when the new
// text has no block tags of its own.
var javaKeptTags = []string{"@param", "@return", "@throws", "@exception", "@since", "@author", "@deprecated", "@see"}

func (javaLang) symbolDoc(src []byte, sym extract.Symbol, text string) ([]byte, string, error) {
	return blockComment(src, sym, withKeptTags(text, sym.Doc, javaKeptTags))
}

// withKeptTags appends the block tags of the old doc named in keptTags
// to text when text has no block tags.
func withKeptTags(text string, old *extract.Doc, keptTags []string) string {
	if old == nil {
		return text
	}
	for _, l := range strings.Split(text, "\n") {
		if isBlockTag(strings.TrimSpace(l)) {
			return text
		}
	}
	var kept []string
	keep := false
	for _, l := range strings.Split(old.Text, "\n") {
		if t := strings.TrimSpace(l); isBlockTag(t) {
			name, _, _ := strings.Cut(t, " ")
			keep = slices.Contains(keptTags, name)
		}
		if keep {
			kept = append(kept, l)
		}
	}
	if len(kept) == 0 {
		return text
	}
	return text + "\n\n" + strings.Join(kept, "\n")
}

// moduleDoc writes the Javadoc of package-info.java, creating the file with
// the package declaration when absent.
func (javaLang) moduleDoc(t Target, text string) (Edit, error) {
	if strings.Contains(text, "*/") {
		return Edit{}, fmt.Errorf("write: doc text for %s contains */", t.ModuleDir)
	}
	path := moduleFile(t.ModuleDir, "package-info.java")
	src, existed, err := readExisting(t.Root, path)
	if err != nil {
		return Edit{}, err
	}
	lines := blockLines(text, "")
	if !existed {
		pkg, err := packageName(t.Root, t.ModuleDir, parse.Java, ".java")
		if err != nil {
			return Edit{}, err
		}
		out := strings.Join(lines, "\n") + "\npackage " + pkg + ";\n"
		return Edit{Path: path, After: []byte(out), Want: text}, nil
	}
	e := Edit{Path: path, Existed: true, Before: src, Want: text}
	res, err := extractSource(parse.Java, path, src)
	if err != nil {
		return Edit{}, err
	}
	if d := res.ModuleDoc; d != nil {
		e.After = replaceSpan(src, d.Span.StartByte, d.Span.EndByte, lines, "")
		return e, nil
	}
	at, ok, err := topLevelStart(parse.Java, src, "package_declaration")
	if err != nil {
		return Edit{}, err
	}
	if !ok {
		return Edit{}, fmt.Errorf("write: %s has no package declaration", path)
	}
	e.After = insertAbove(src, at, lines, "")
	return e, nil
}

func (javaLang) readModuleDoc(path string, src []byte) (*extract.Doc, int, error) {
	return moduleFileDoc(parse.Java, path, src)
}

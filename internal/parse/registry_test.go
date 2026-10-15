package parse_test

import (
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/parse"
)

func TestFamilies(t *testing.T) {
	for lang, want := range map[string]string{
		"go": "go", "typescript": "ts", "tsx": "ts", "javascript": "ts", "ts": "ts",
		"python": "py", "py": "py", "java": "java", "bash": "", "cobol": "",
	} {
		if got := parse.Family(lang); got != want {
			t.Errorf("Family(%s) = %q, want %q", lang, got, want)
		}
	}
	if got := strings.Join(parse.Families(), ","); got != "go,java,py,ts" {
		t.Fatal(got)
	}
	if got := strings.Join(parse.Languages(), ","); got != "go,java,javascript,python,tsx,typescript" {
		t.Fatal(got)
	}
	for fam, want := range map[string]string{
		"ts": "javascript,tsx,typescript", "go": "go", "py": "python", "tsx": "tsx", "cobol": "",
	} {
		if got := strings.Join(parse.LanguagesOf(fam), ","); got != want {
			t.Errorf("LanguagesOf(%s) = %q, want %q", fam, got, want)
		}
	}
}

func TestDocumentLanguages(t *testing.T) {
	for p, want := range map[string]string{"README.md": parse.Markdown, "a/b.MDX": parse.Markdown, "x.markdown": parse.Markdown,
		"n.rst": parse.RST, "g.adoc": parse.ASCIIDoc, "CHANGES.txt": parse.Text, "main.go": "", "Makefile": ""} {
		if got := parse.DocLanguageForPath(p); got != want {
			t.Errorf("DocLanguageForPath(%q) = %q, want %q", p, got, want)
		}
	}
	if parse.LanguageForPath("README.md") != "" {
		t.Error("a document is no code language")
	}
	for _, lang := range append(parse.DocLanguages(), parse.DocFamily) {
		if !parse.IsDoc(lang) || parse.Family(lang) != parse.DocFamily {
			t.Errorf("%s is no document language", lang)
		}
	}
	if parse.IsDoc(parse.Go) || parse.IsDoc("") {
		t.Error("go or no language is a document language")
	}
	if got := parse.LanguagesOf(parse.DocFamily); len(got) != 4 {
		t.Errorf("LanguagesOf(doc) = %v", got)
	}
	if got := parse.LanguagesOf(parse.RST); len(got) != 1 || got[0] != parse.RST {
		t.Errorf("LanguagesOf(rst) = %v", got)
	}
}

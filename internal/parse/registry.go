package parse

import (
	"path"
	"slices"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
	tsbash "github.com/tree-sitter/tree-sitter-bash/bindings/go"
	tsgo "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tsjava "github.com/tree-sitter/tree-sitter-java/bindings/go"
	tspy "github.com/tree-sitter/tree-sitter-python/bindings/go"
	tsts "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

// Precise language names, as stored on file rows.
const (
	Go         = "go"
	TypeScript = "typescript"
	TSX        = "tsx"
	JavaScript = "javascript"
	Python     = "python"
	Java       = "java"
	// Bash is parsed only for config extraction (shell scripts); it has no
	// extension mapping because config formats are detected by the cfg
	// package.
	Bash = "bash"
)

// Document languages (documents spec section 2). They have no grammar:
// internal/extract/doc reads their headings line by line.
const (
	Markdown = "markdown"
	RST      = "rst"
	ASCIIDoc = "asciidoc"
	Text     = "text"
)

// DocFamily is the family of the document languages, as stored in the
// stable keys of documents and sections.
const DocFamily = "doc"

var docByExt = map[string]string{
	".md": Markdown, ".mdx": Markdown, ".markdown": Markdown, ".rst": RST, ".adoc": ASCIIDoc, ".txt": Text,
}

var langByExt = map[string]string{
	".go": Go, ".ts": TypeScript, ".mts": TypeScript, ".cts": TypeScript, ".tsx": TSX,
	".js": JavaScript, ".jsx": JavaScript, ".mjs": JavaScript, ".cjs": JavaScript,
	".py": Python, ".java": Java,
}

// grammars maps a precise language name to its grammar. JavaScript uses the
// TSX grammar, which accepts plain JavaScript and JSX alike;
// tree-sitter-javascript can be added later if the TypeScript grammars prove
// too strict for some JavaScript sources.
var grammars = map[string]*sitter.Language{
	Go:         sitter.NewLanguage(tsgo.Language()),
	TypeScript: sitter.NewLanguage(tsts.LanguageTypescript()),
	TSX:        sitter.NewLanguage(tsts.LanguageTSX()),
	JavaScript: sitter.NewLanguage(tsts.LanguageTSX()),
	Python:     sitter.NewLanguage(tspy.Language()),
	Java:       sitter.NewLanguage(tsjava.Language()),
	Bash:       sitter.NewLanguage(tsbash.Language()),
}

// families maps each precise language, and each family name, to its
// family.
var families = map[string]string{
	Go: "go", TypeScript: "ts", TSX: "ts", JavaScript: "ts", Python: "py", Java: "java",
	"ts": "ts", "py": "py",
	Markdown: DocFamily, RST: DocFamily, ASCIIDoc: DocFamily, Text: DocFamily, DocFamily: DocFamily,
}

// precise lists the precise languages that belong to a family, sorted.
var precise = []string{Go, Java, JavaScript, Python, TSX, TypeScript}

// LanguageForPath returns the precise language for a file path by
// extension, or "" when no parser handles it.
func LanguageForPath(p string) string {
	return langByExt[strings.ToLower(path.Ext(p))]
}

// DocLanguageForPath returns the document language of a file path by
// extension, or "" when the file is no document.
func DocLanguageForPath(p string) string {
	return docByExt[strings.ToLower(path.Ext(p))]
}

// DocLanguages returns the document languages, sorted.
func DocLanguages() []string {
	return []string{ASCIIDoc, Markdown, RST, Text}
}

// IsDoc reports whether lang is a document language or their family.
func IsDoc(lang string) bool {
	return families[lang] == DocFamily
}

// Family returns the language family used in stable keys and module rows
// (spec 5.1): "ts" for TypeScript, TSX and JavaScript, "go" for Go, "py"
// for Python, "java" for Java, "doc" for the document languages, and ""
// for unknown languages. A family
// name is its own family.
func Family(lang string) string {
	return families[lang]
}

// Families returns the code language families, sorted: go, java, py, ts.
func Families() []string {
	return []string{"go", "java", "py", "ts"}
}

// Languages returns the precise languages of every code family, sorted.
func Languages() []string {
	return append([]string(nil), precise...)
}

// LanguagesOf returns the precise languages of family fam, sorted; a
// precise language name yields itself, an unknown name nothing.
func LanguagesOf(fam string) []string {
	if fam == DocFamily {
		return DocLanguages()
	}
	if !slices.Contains(Families(), fam) {
		if slices.Contains(precise, fam) || IsDoc(fam) {
			return []string{fam}
		}
		return nil
	}
	var out []string
	for _, l := range precise {
		if families[l] == fam {
			out = append(out, l)
		}
	}
	return out
}

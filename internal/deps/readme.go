package deps

import (
	"os"
	"strings"
)

// maxReadme caps the README text stored as a root module doc.
const maxReadme = 32 << 10

// readmeNames are the README file names looked for, lower case, in order.
var readmeNames = []string{"readme.md", "readme.markdown", "readme", "readme.txt"}

// readme returns the text of the README at the root of a package and its
// file name; both empty when there is none. Text beyond maxReadme is cut
// at a line end, and no more than that is read of the file. A README
// that is a link to a file outside the package is not read.
func readme(dir string) (text, name string) {
	name = readmeName(dir)
	if name == "" {
		return "", ""
	}
	full, err := within(dir, name)
	if err != nil {
		return "", ""
	}
	raw, err := readHead(full, maxReadme+1)
	if err != nil {
		return "", ""
	}
	if text = clip(strings.TrimSpace(string(raw))); text == "" {
		return "", ""
	}
	return text, name
}

// readmeName returns the name of the README file of a directory, ""
// when it has none.
func readmeName(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	byLower := map[string]string{}
	for _, e := range entries {
		if !e.IsDir() {
			byLower[strings.ToLower(e.Name())] = e.Name()
		}
	}
	for _, want := range readmeNames {
		if name, ok := byLower[want]; ok {
			return name
		}
	}
	return ""
}

// clip cuts text to maxReadme bytes at the last line end before it.
func clip(text string) string {
	if len(text) <= maxReadme {
		return text
	}
	cut := text[:maxReadme]
	if i := strings.LastIndexByte(cut, '\n'); i > 0 {
		cut = cut[:i]
	}
	return strings.ToValidUTF8(cut, "")
}

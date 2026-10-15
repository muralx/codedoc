package deps

import (
	"bufio"
	"os"
	"regexp"
	"strings"
)

// referencePath matches a triple-slash directive naming another file of
// the package: /// <reference path="fs.d.ts" />.
var referencePath = regexp.MustCompile(`^///\s*<reference\s+path\s*=\s*["']([^"']+)["']`)

// referencePaths returns the files the reference directives of a
// declaration file name, as relative specifiers. Directives stand before
// any statement, so reading stops at the first line that is neither a
// comment nor blank. A file that resolves outside the package is not
// opened (see within).
func referencePaths(root, rel string) []string {
	full, err := within(root, rel)
	if err != nil {
		return nil
	}
	f, err := os.Open(full)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	var out []string
	sc := bufio.NewScanner(f)
	block := false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if m := referencePath.FindStringSubmatch(line); m != nil {
			spec := m[1]
			if !strings.HasPrefix(spec, ".") {
				spec = "./" + spec
			}
			out = append(out, spec)
			continue
		}
		switch {
		case block:
			block = !strings.Contains(line, "*/")
		case strings.HasPrefix(line, "/*"):
			block = !strings.Contains(line, "*/")
		case line != "" && !strings.HasPrefix(line, "//"):
			return out
		}
	}
	return out
}

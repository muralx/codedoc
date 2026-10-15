package config

import (
	"fmt"
	"sort"
	"strings"
)

// languageValues maps each value a repo's `languages:` accepts to the
// file languages it selects: the precise language names stored on file
// rows (see parse) and the config formats (see extract/cfg). A value is
// a language name, a family (ts, py, doc) or a config format.
var languageValues = map[string][]string{
	"go": {"go"}, "ts": {"typescript", "tsx", "javascript"}, "typescript": {"typescript", "tsx"},
	"javascript": {"javascript"}, "py": {"python"}, "python": {"python"}, "java": {"java"},
	"yaml": {"yaml"}, "json": {"json"}, "xml": {"xml"}, "properties": {"properties"}, "env": {"env"},
	"hcl": {"hcl"}, "shell": {"shell"}, "template": {"template"},
	"doc": {"markdown", "rst", "asciidoc", "text"}, "markdown": {"markdown"}, "rst": {"rst"},
	"asciidoc": {"asciidoc"}, "text": {"text"},
}

// FileLanguages returns the file languages the repo's `languages:`
// selects, nil when it lists none: every language is indexed.
func (r *Repo) FileLanguages() map[string]bool {
	if len(r.Languages) == 0 {
		return nil
	}
	out := map[string]bool{}
	for _, v := range r.Languages {
		for _, l := range languageValues[v] {
			out[l] = true
		}
	}
	return out
}

// validateLanguages rejects a `languages:` value no language matches.
func validateLanguages(r *Repo) error {
	for _, v := range r.Languages {
		if _, ok := languageValues[v]; !ok {
			known := make([]string, 0, len(languageValues))
			for k := range languageValues {
				known = append(known, k)
			}
			sort.Strings(known)
			return fmt.Errorf("repo %s: unknown language %q (known: %s)", r.Name, v, strings.Join(known, ", "))
		}
	}
	return nil
}

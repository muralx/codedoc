package doc

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// frontMatter is the YAML block a Markdown document may start with.
// lines counts the lines it takes, delimiters included; 0 without one.
type frontMatter struct {
	title, description string
	lines              int
}

// readFrontMatter reads the block between a first line "---" and the
// next line "---" or "...". A block that never closes is no front
// matter: its text is content. A block that is not valid YAML (tools
// write lines such as "argument-hint: [a] [b]") is still skipped, and
// its title and description are read line by line.
func readFrontMatter(lines []string) frontMatter {
	if len(lines) == 0 || strings.TrimRight(lines[0], " \t") != "---" {
		return frontMatter{}
	}
	for i := 1; i < len(lines); i++ {
		if t := strings.TrimRight(lines[i], " \t"); t != "---" && t != "..." {
			continue
		}
		var fields struct {
			Title       string `yaml:"title"`
			Description string `yaml:"description"`
		}
		if err := yaml.Unmarshal([]byte(strings.Join(lines[1:i], "\n")), &fields); err != nil {
			fields.Title, fields.Description = plainField(lines[1:i], "title"), plainField(lines[1:i], "description")
		}
		return frontMatter{title: collapse(fields.Title), description: strings.TrimSpace(fields.Description),
			lines: i + 1}
	}
	return frontMatter{}
}

// plainField returns the value of the first line "name: value" of a
// front matter block that YAML could not parse, without its quotes.
func plainField(lines []string, name string) string {
	for _, l := range lines {
		if v, ok := strings.CutPrefix(l, name+":"); ok {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}

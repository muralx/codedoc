package resolve

import (
	"encoding/xml"
	"regexp"
	"strings"
)

// tomlName matches a name = "..." line of a TOML table.
var tomlName = regexp.MustCompile(`^name\s*=\s*["']([^"']+)["']`)

// pyprojectName returns the distribution name of a pyproject.toml: the
// name of its [project] table, else of its [tool.poetry] table, or "".
func pyprojectName(data []byte) string {
	names := map[string]string{}
	table := ""
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			table = strings.Trim(line, "[] ")
			continue
		}
		if m := tomlName.FindStringSubmatch(line); m != nil && names[table] == "" {
			names[table] = m[1]
		}
	}
	if n := names["project"]; n != "" {
		return n
	}
	return names["tool.poetry"]
}

// setupKeyword matches the name= keyword argument of a setup() call.
var setupKeyword = regexp.MustCompile(`\bname\s*=\s*["']([^"']+)["']`)

// setupName returns the name= argument of a setup.py, or "".
func setupName(data []byte) string {
	if m := setupKeyword.FindSubmatch(data); m != nil {
		return string(m[1])
	}
	return ""
}

// pomIdentity returns groupId/artifactId of a pom.xml, the groupId taken
// from the parent when the project has none; a pom without groupId is
// identified by its artifactId alone, one that does not parse by "".
func pomIdentity(data []byte) string {
	var pom struct {
		GroupID    string `xml:"groupId"`
		ArtifactID string `xml:"artifactId"`
		Parent     struct {
			GroupID string `xml:"groupId"`
		} `xml:"parent"`
	}
	if xml.Unmarshal(data, &pom) != nil || pom.ArtifactID == "" {
		return ""
	}
	group := pom.GroupID
	if group == "" {
		group = pom.Parent.GroupID
	}
	if group == "" {
		return pom.ArtifactID
	}
	return group + "/" + pom.ArtifactID
}

// gradleRoot matches rootProject.name = "..." in Groovy or Kotlin DSL.
var gradleRoot = regexp.MustCompile(`rootProject\.name\s*=\s*["']([^"']+)["']`)

// gradleName returns the rootProject.name of a settings.gradle(.kts), or "".
func gradleName(data []byte) string {
	if m := gradleRoot.FindSubmatch(data); m != nil {
		return string(m[1])
	}
	return ""
}

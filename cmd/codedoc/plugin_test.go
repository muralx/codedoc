package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// repoRoot is the repository root seen from this package's directory.
const repoRoot = "../.."

func readJSON(t *testing.T, rel string, v any) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot, rel))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
}

func TestPluginManifests(t *testing.T) {
	var plugin struct {
		Name, Description, Version string
		Author                     struct{ Name string }
	}
	readJSON(t, "plugin/.claude-plugin/plugin.json", &plugin)
	if plugin.Name != "codedoc" || plugin.Description == "" || plugin.Version == "" || plugin.Author.Name == "" {
		t.Fatalf("plugin.json: %+v", plugin)
	}
	var servers map[string]struct {
		Type, Command string
		Args          []string
	}
	readJSON(t, "plugin/.mcp.json", &servers)
	if s := servers["codedoc"]; s.Type != "stdio" || s.Command != "codedoc" || strings.Join(s.Args, " ") != "mcp" {
		t.Fatalf(".mcp.json: %+v", servers)
	}
	var market struct {
		Name    string
		Plugins []struct{ Name, Source string }
	}
	readJSON(t, ".claude-plugin/marketplace.json", &market)
	if market.Name == "" || len(market.Plugins) != 1 || market.Plugins[0].Name != "codedoc" ||
		market.Plugins[0].Source != "./plugin" {
		t.Fatalf("marketplace.json: %+v", market)
	}
}

// skills returns the SKILL.md files by skill directory name.
func skills(t *testing.T) map[string]string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(repoRoot, "plugin", "skills", "*", "SKILL.md"))
	if err != nil || len(paths) != 3 {
		t.Fatalf("%v %v", err, paths)
	}
	out := map[string]string{}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		out[filepath.Base(filepath.Dir(p))] = string(b)
	}
	return out
}

// splitFrontMatter decodes the YAML front matter of a skill or agent file
// into fm and returns the text after it.
func splitFrontMatter(name, text string, fm any) (string, error) {
	parts := strings.SplitN(text, "---\n", 3)
	if len(parts) != 3 || parts[0] != "" {
		return "", fmt.Errorf("%s: no front matter", name)
	}
	if err := yaml.Unmarshal([]byte(parts[1]), fm); err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	return parts[2], nil
}

func TestSkillFrontMatter(t *testing.T) {
	for dir, text := range skills(t) {
		var fm struct{ Name, Description string }
		body, err := splitFrontMatter(dir, text, &fm)
		if err != nil {
			t.Fatal(err)
		}
		if fm.Name != dir || !strings.HasPrefix(fm.Description, "Use when") {
			t.Fatalf("%s: %+v", dir, fm)
		}
		if n := strings.Count(body, "\n"); n >= 150 {
			t.Fatalf("%s: body has %d lines", dir, n)
		}
		if strings.Contains(strings.TrimSpace(fm.Description), "\n") || len(fm.Description) > 1024 {
			t.Fatalf("%s: description is not one paragraph of at most 1024 bytes", dir)
		}
	}
}

// TestSkillSizes holds the two skills a session carries to the fixed cost
// of the efficiency spec, section 9: lookup at most 60 lines, document at
// most 40, and together under 1,200 tokens estimated as characters / 4.
func TestSkillSizes(t *testing.T) {
	all := skills(t)
	chars := 0
	for dir, limit := range map[string]int{"codedoc-lookup": 60, "codedoc-document": 40} {
		if n := strings.Count(all[dir], "\n"); n > limit {
			t.Errorf("%s has %d lines, at most %d", dir, n, limit)
		}
		chars += utf8.RuneCountInString(all[dir])
	}
	if chars >= 4800 {
		t.Errorf("lookup and document hold %d characters (%d tokens), need under 4800 (1200)", chars, chars/4)
	}
}

// TestLookupSkillTriggersOnConcreteSituations checks the lookup skill's
// description names the habits it replaces (the usage study found it never
// loaded), that its body holds the table, the delegate rule and the
// cross-check and nothing about how the tool works, and that the other
// skills send code reading to it.
func TestLookupSkillTriggersOnConcreteSituations(t *testing.T) {
	all := skills(t)
	var fm struct{ Description string }
	body, err := splitFrontMatter("codedoc-lookup", all["codedoc-lookup"], &fm)
	if err != nil {
		t.Fatal(err)
	}
	for _, trigger := range []string{"read or search", "grep", "sed", "cat", "Read", "where something is",
		"reading a function", "who calls it", "what a change breaks", "dependency"} {
		if !strings.Contains(fm.Description, trigger) {
			t.Errorf("codedoc-lookup description lacks %q", trigger)
		}
	}
	for _, want := range []string{"codedoc symbol", "codedoc outline", "codedoc impact", "codedoc where", "codedoc how",
		"codedoc api", "codedoc ls", "codedoc lines", "codedoc feedback", "codedoc-explorer"} {
		if !strings.Contains(body, want) {
			t.Errorf("codedoc-lookup lacks %q", want)
		}
	}
	for _, banned := range []string{"stable key", "--budget", "fresh:", "ambiguous", "precise"} {
		if strings.Contains(body, banned) {
			t.Errorf("codedoc-lookup explains the tool: %q", banned)
		}
	}
	for _, dir := range []string{"codedoc-document", "codedoc-maintain"} {
		if !strings.Contains(all[dir], "codedoc-lookup skill") {
			t.Errorf("%s does not point to the codedoc-lookup skill", dir)
		}
	}
}

// TestDocumentSkillAsksForDocsAtWriteTime checks the document skill tells
// the session to write the comment with the code, names the two ways to
// document code that is not being edited, and never sends it looking for
// stale docs.
func TestDocumentSkillAsksForDocsAtWriteTime(t *testing.T) {
	text := skills(t)["codedoc-document"]
	for _, want := range []string{"always", "only this session knows", "codedoc doc set", "codedoc-documenter",
		"Function", "Type", "Module"} {
		if !strings.Contains(text, want) {
			t.Errorf("codedoc-document lacks %q", want)
		}
	}
	for _, banned := range []string{"codedoc changes", "codedoc doc check", "codedoc gaps", "not obvious", "only where"} {
		if strings.Contains(text, banned) {
			t.Errorf("codedoc-document holds %q", banned)
		}
	}
}

// agents returns the agent files of the plugin by file name without ".md".
func agents(t *testing.T) map[string]string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(repoRoot, "plugin", "agents", "*.md"))
	if err != nil || len(paths) != 2 {
		t.Fatalf("%v %v", err, paths)
	}
	out := map[string]string{}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		out[strings.TrimSuffix(filepath.Base(p), ".md")] = string(b)
	}
	return out
}

// agentModels are the model aliases a Claude Code subagent accepts;
// agentTools the only tools a codedoc agent may hold: it reads through
// codedoc and never edits. The tools field takes tool names only, so the
// body of the agent is what confines Bash to codedoc.
var (
	agentModels = map[string]bool{"haiku": true, "sonnet": true, "opus": true, "fable": true, "inherit": true}
	agentTools  = map[string]bool{"Bash": true, "Read": true}
)

// checkAgent validates one agent file: front matter with name,
// description, tools and model, the name being the file's, a model alias,
// tools from the allowed set, and a body that confines Bash to codedoc.
func checkAgent(file, text string) error {
	var fm struct{ Name, Description, Tools, Model string }
	body, err := splitFrontMatter(file, text, &fm)
	if err != nil {
		return err
	}
	if fm.Name != file {
		return fmt.Errorf("%s: name is %q", file, fm.Name)
	}
	if fm.Description == "" || strings.Contains(strings.TrimSpace(fm.Description), "\n") {
		return fmt.Errorf("%s: description is not one paragraph", file)
	}
	if !agentModels[fm.Model] {
		return fmt.Errorf("%s: model %q is no model alias", file, fm.Model)
	}
	if fm.Tools == "" {
		return fmt.Errorf("%s: no tools", file)
	}
	for _, tool := range strings.Split(fm.Tools, ",") {
		if !agentTools[strings.TrimSpace(tool)] {
			return fmt.Errorf("%s: tool %q is not allowed", file, strings.TrimSpace(tool))
		}
	}
	if !strings.Contains(body, "only `codedoc` commands") {
		return fmt.Errorf("%s: the body does not confine Bash to codedoc", file)
	}
	return nil
}

func TestAgentFrontMatter(t *testing.T) {
	all := agents(t)
	for _, name := range []string{"codedoc-explorer", "codedoc-documenter"} {
		if _, ok := all[name]; !ok {
			t.Fatalf("no agent %s", name)
		}
	}
	for name, text := range all {
		if err := checkAgent(name, text); err != nil {
			t.Error(err)
		}
		if n := strings.Count(text, "\n"); n > 60 {
			t.Errorf("%s has %d lines, at most 60", name, n)
		}
	}
	for _, want := range []string{"300 words", "path:line", "checked:", "not found:", "Never guess"} {
		if !strings.Contains(all["codedoc-explorer"], want) {
			t.Errorf("codedoc-explorer lacks %q", want)
		}
	}
	for _, want := range []string{"codedoc doc set --batch", "=== ", "count", "Function", "Type", "Module"} {
		if !strings.Contains(all["codedoc-documenter"], want) {
			t.Errorf("codedoc-documenter lacks %q", want)
		}
	}
}

func TestCheckAgentRejectsBadFrontMatter(t *testing.T) {
	const body = "---\nUse only `codedoc` commands.\n"
	good := "---\nname: a\ndescription: d\ntools: Bash, Read\nmodel: haiku\n" + body
	if err := checkAgent("a", good); err != nil {
		t.Fatal(err)
	}
	for what, text := range map[string]string{
		"no front matter": "name: a\n",
		"bad yaml":        "---\nname: [\n" + body,
		"other name":      strings.Replace(good, "name: a", "name: b", 1),
		"no description":  strings.Replace(good, "description: d\n", "", 1),
		"model id":        strings.Replace(good, "haiku", "gpt4", 1),
		"no tools":        strings.Replace(good, "tools: Bash, Read\n", "", 1),
		"editing tool":    strings.Replace(good, "Bash, Read", "Bash, Edit", 1),
		"scoped tool":     strings.Replace(good, "Bash, Read", "Bash(codedoc:*), Read", 1),
		"free shell":      strings.Replace(good, "only `codedoc` commands", "any command", 1),
	} {
		if err := checkAgent("a", text); err == nil {
			t.Errorf("%s accepted", what)
		}
	}
}

var (
	inlineCode = regexp.MustCompile("`[^`\n]+`")
	cliCall    = regexp.MustCompile(`\bcodedoc ([a-z][a-z-]*)(?: ([a-z][a-z-]*))?`)
)

// codeText returns the fenced blocks and inline code spans of a markdown
// text: the places where a skill shows a command.
func codeText(md string) []string {
	var out []string
	fenced := false
	for _, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			continue
		}
		if fenced {
			out = append(out, line)
			continue
		}
		out = append(out, inlineCode.FindAllString(line, -1)...)
	}
	return out
}

// TestSkillsAndAgentsReferenceRealCommands checks that every codedoc
// command a skill or an agent shows is a command of this binary.
func TestSkillsAndAgentsReferenceRealCommands(t *testing.T) {
	root := newRootCmd()
	texts := skills(t)
	for name, text := range agents(t) {
		texts["agents/"+name] = text
	}
	seen := 0
	for name, text := range texts {
		n := 0
		for _, code := range codeText(text) {
			for _, m := range cliCall.FindAllStringSubmatch(code, -1) {
				n++
				if err := checkCommand(root, m[1], m[2]); err != nil {
					t.Errorf("%s: %q: %v", name, m[0], err)
				}
			}
		}
		if n == 0 {
			t.Errorf("%s shows no codedoc command", name)
		}
		seen += n
	}
	if seen < 30 {
		t.Fatalf("only %d codedoc commands found in the skills and agents", seen)
	}
}

// checkCommand verifies that name is a subcommand of root and, when it has
// subcommands of its own and does not run by itself, that sub is one of
// them (codedoc deps runs with a module or with a subcommand).
func checkCommand(root *cobra.Command, name, sub string) error {
	cmd, _, err := root.Find([]string{name})
	if err != nil || cmd == root {
		return errUnknown(name)
	}
	if !cmd.HasSubCommands() || cmd.Runnable() {
		return nil
	}
	if c, _, err := cmd.Find([]string{sub}); err != nil || c == cmd {
		return errUnknown(name + " " + sub)
	}
	return nil
}

type errUnknown string

func (e errUnknown) Error() string { return "no command codedoc " + string(e) }

func TestCheckCommandRejectsUnknownCommands(t *testing.T) {
	root := newRootCmd()
	for _, c := range [][2]string{{"nope", ""}, {"doc", "bogus"}, {"scip", ""}} {
		if err := checkCommand(root, c[0], c[1]); err == nil {
			t.Errorf("%v accepted", c)
		}
	}
	for _, c := range [][2]string{{"status", ""}, {"doc", "set"}, {"scip", "run"}, {"gaps", "github"}, {"mcp", ""},
		{"deps", ""}, {"deps", "index"}, {"deps", "prune"}, {"impact", ""}, {"where", ""}, {"how", ""}, {"api", ""}} {
		if err := checkCommand(root, c[0], c[1]); err != nil {
			t.Errorf("%v: %v", c, err)
		}
	}
}

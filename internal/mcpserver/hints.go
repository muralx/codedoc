package mcpserver

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

// toolForm says how a CLI command is called as a tool: the tool, the
// inputs the command word fixes (the relation of codedoc_graph), the
// inputs its positional arguments are, in order, and the list input
// that takes them all when there are more (or no named ones). in is
// the zero input of the tool: its fields say which flags it has.
type toolForm struct {
	tool  string
	in    any
	fixed [][2]string
	pos   []string
	rest  string
}

// toolForms maps the command words an answer may name to tools.
var toolForms = func() map[string]toolForm {
	forms := map[string]toolForm{
		"overview": {tool: "codedoc_overview", in: OverviewInput{}},
		"outline":  {tool: "codedoc_outline", in: OutlineInput{}, pos: []string{"target"}},
		"ls":       {tool: "codedoc_ls", in: LsInput{}, rest: "paths"},
		"lines":    {tool: "codedoc_lines", in: LinesInput{}, pos: []string{"path", "range"}},
		"find":     {tool: "codedoc_find", in: FindInput{}, pos: []string{"pattern"}},
		"symbol":   {tool: "codedoc_symbol", in: SymbolInput{}, pos: []string{"symbol"}, rest: "names"},
		"impact":   {tool: "codedoc_impact", in: ImpactInput{}, pos: []string{"symbol"}},
		"how":      {tool: "codedoc_how", in: HowInput{}, pos: []string{"target"}},
		"api":      {tool: "codedoc_api", in: APIInput{}, pos: []string{"package", "name"}},
		"config":   {tool: "codedoc_config", in: ConfigInput{}, pos: []string{"key"}},
		"search":   {tool: "codedoc_search", in: SearchInput{}, pos: []string{"query"}},
		"where":    {tool: "codedoc_where", in: WhereInput{}, pos: []string{"query"}},
		"explain":  {tool: "codedoc_explain", in: ExplainInput{}, pos: []string{"target"}},
		"flow":     {tool: "codedoc_flow", in: FlowInput{}, pos: []string{"symbol"}},
		"feedback": {tool: "codedoc_feedback", in: FeedbackInput{}},
		"gaps":     {tool: "codedoc_doc", in: DocInput{}, fixed: [][2]string{{"action", "gaps"}}, pos: []string{"target"}},
	}
	for _, mode := range enums["mode"] {
		forms[mode] = toolForm{tool: "codedoc_history", in: HistoryInput{}, fixed: [][2]string{{"mode", mode}}, pos: []string{"symbol"}}
	}
	for _, relation := range enums["relation"] {
		forms[relation] = toolForm{tool: "codedoc_graph", in: GraphInput{}, fixed: [][2]string{{"relation", relation}},
			pos: []string{"target"}}
	}
	return forms
}()

// depsList is the form of codedoc deps without a module: the listing
// of the packages, not the relation of codedoc_graph.
var depsList = toolForm{tool: "codedoc_deps", in: DepsInput{}}

// inputKinds returns the kinds of the inputs of a tool by name, those
// of its embedded input groups included.
func inputKinds(t reflect.Type, into map[string]reflect.Kind) map[string]reflect.Kind {
	for i := range t.NumField() {
		f := t.Field(i)
		if f.Anonymous {
			inputKinds(f.Type, into)
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		kind := f.Type.Kind()
		if kind == reflect.Pointer {
			kind = f.Type.Elem().Kind()
		}
		into[name] = kind
	}
	return into
}

// omission matches a line that names the command printing what an
// answer left out: "8 more: codedoc ...", "... 2 more sections (a, b):
// codedoc ...", "12 more lines: codedoc lines ...".
var omission = regexp.MustCompile(`(?m)^(\s*(?:\.\.\. )?\d+ more.*?: )(codedoc [^\n]+)$`)

// toolHints rewrites the omission lines of an answer for a caller that
// has tools, not a shell: the command that prints the rest becomes the
// tool and the inputs that do ("8 more: codedoc_find
// {"pattern":"Get","offset":3}"). The workspace input of the call that
// was answered goes with every one of them: a command that continues a
// source names its file, not the workspace. A command no tool runs
// with the same meaning is left as it is.
func toolHints(text, workspace string) string {
	return omission.ReplaceAllStringFunc(text, func(line string) string {
		m := omission.FindStringSubmatch(line)
		if call, ok := toolCall(m[2], workspace); ok {
			return m[1] + call
		}
		return line
	})
}

// toolCall translates a CLI command line, as answers print it, into
// the tool call that runs it: the tool name and its inputs as JSON, in
// the order of the command line. ok is false for a command without a
// tool, or with a flag its tool has no input for. workspace, when set,
// is added to a call that names none.
func toolCall(command, workspace string) (call string, ok bool) {
	words := shellWords(command)
	if len(words) < 2 || words[0] != "codedoc" {
		return "", false
	}
	form, args, known := formOf(words[1], words[2:])
	if !known {
		return "", false
	}
	in := inputs{kinds: inputKinds(reflect.TypeOf(form.in), map[string]reflect.Kind{})}
	for _, kv := range form.fixed {
		in.set(kv[0], kv[1])
	}
	if !in.read(form, args) {
		return "", false
	}
	if workspace != "" && indexOf(args, "--workspace") < 0 {
		in.set("workspace", workspace)
	}
	return form.tool + " {" + strings.Join(in.fields, ",") + "}", true
}

// formOf returns the form of the tool that runs a command and the
// arguments to read as its inputs; known is false without one.
// codedoc tests --transitive, which codedoc_graph cannot ask, is the
// tests section of codedoc_impact, the same list; codedoc deps without
// a module is the listing of the packages.
func formOf(name string, args []string) (form toolForm, rest []string, known bool) {
	if i := indexOf(args, "--transitive"); name == "tests" && i >= 0 {
		name, args = "impact", append(append(append([]string{}, args[:i]...), args[i+1:]...), "--only", "tests")
	}
	if name == "deps" && (len(args) == 0 || strings.HasPrefix(args[0], "--")) {
		return depsList, args, true
	}
	form, known = toolForms[name]
	return form, args, known
}

// inputs collects the inputs of a tool call as JSON fields.
type inputs struct {
	kinds  map[string]reflect.Kind
	fields []string
}

// set adds an input.
func (in *inputs) set(name string, v any) {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false) // a path is printed as it is written
	if err := enc.Encode(v); err != nil {
		return // strings, numbers and booleans always encode
	}
	in.fields = append(in.fields, strconv.Quote(name)+":"+strings.TrimSuffix(b.String(), "\n"))
}

// read adds the inputs the arguments of a command are: its positional
// arguments by the names of form, its flags by their own (--no-body is
// no_body), typed as the tool declares them. It is false when the tool
// has no input for one of them.
func (in *inputs) read(form toolForm, args []string) bool {
	var pos []string
	var flags [][2]string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "--") || a == "--" {
			pos = append(pos, a)
			continue
		}
		name, value, _ := strings.Cut(strings.ReplaceAll(strings.TrimPrefix(a, "--"), "-", "_"), "=")
		kind, known := in.kinds[name]
		switch {
		case !known:
			return false
		case kind != reflect.Bool && !strings.Contains(a, "=") && i+1 < len(args):
			i++
			value = args[i]
		}
		flags = append(flags, [2]string{name, value})
	}
	if !in.positional(form, pos) {
		return false
	}
	for _, f := range flags {
		in.typed(f[0], f[1])
	}
	return true
}

// typed adds the input a flag is, with the type the tool declares.
func (in *inputs) typed(name, value string) {
	switch n, err := strconv.Atoi(value); {
	case in.kinds[name] == reflect.Bool:
		in.set(name, true)
	case in.kinds[name] == reflect.Int && err == nil:
		in.set(name, n)
	default:
		in.set(name, value)
	}
}

// positional adds the positional arguments of a command under the
// names of form; false when it has more than the tool takes.
func (in *inputs) positional(form toolForm, pos []string) bool {
	switch {
	case len(pos) <= len(form.pos):
		for i, v := range pos {
			in.set(form.pos[i], v)
		}
	case form.rest != "":
		in.set(form.rest, pos)
	default:
		return false
	}
	return true
}

// indexOf is the index of word in words, -1 when it is not there.
func indexOf(words []string, word string) int {
	for i, w := range words {
		if w == word {
			return i
		}
	}
	return -1
}

// shellWords splits a command line as answers quote it: words apart
// at spaces, a word in single quotes whole, '\” a quote inside one.
func shellWords(line string) []string {
	var words []string
	var cur strings.Builder
	quoted, open := false, false
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case c == '\'':
			quoted, open = !quoted, true
		case c == '\\' && !quoted && i+1 < len(line):
			i++
			cur.WriteByte(line[i])
		case c == ' ' && !quoted:
			if cur.Len() > 0 || open {
				words = append(words, cur.String())
			}
			cur.Reset()
			open = false
		default:
			cur.WriteByte(c)
		}
	}
	if cur.Len() > 0 || open {
		words = append(words, cur.String())
	}
	return words
}

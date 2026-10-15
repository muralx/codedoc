package mcpserver

import (
	"fmt"
	"strconv"
)

// Common holds the inputs every tool accepts. They map to the CLI flags
// --workspace, --repo, --budget and --offset. The flags an agent seldom
// needs (--format, --out and the filters of each command) are on the CLI
// only: every input costs bytes in each request that lists the tools.
type Common struct {
	Workspace string `json:"workspace,omitempty" jsonschema:"workspace (default: the one around the server's directory)"`
	Repo      string `json:"repo,omitempty" jsonschema:"limit to one repo"`
	Budget    *int   `json:"budget,omitempty" jsonschema:"answer size in bytes (default 6000, at most 60000)"`
	Offset    int    `json:"offset,omitempty" jsonschema:"skip the first N entries of each section"`
}

// common returns the shared inputs; every input type embeds Common.
func (c Common) common() Common { return c }

// flags appends the CLI flags of the shared inputs. The budget defaults
// to byDefault and is capped at maxBudget (0, unlimited on the CLI,
// means maxBudget too: an unbounded answer would flood the agent's
// context).
func (c Common) flags(a *argv, byDefault int) {
	budget := byDefault
	if c.Budget != nil {
		budget = *c.Budget
	}
	if budget == 0 || budget > maxBudget {
		budget = maxBudget
	}
	a.str("workspace", c.Workspace)
	a.str("repo", c.Repo)
	a.flag("--budget=" + strconv.Itoa(budget))
	a.num("offset", c.Offset)
}

// WithDetail is the input of the tools whose text leaves out tags,
// signatures and doc sentences unless asked: --detail.
type WithDetail struct {
	Detail bool `json:"detail,omitempty" jsonschema:"print every tag, signature and doc sentence"`
}

// WithLines is the input of the tools that list lines of files: -C.
type WithLines struct {
	Context string `json:"context,omitempty" jsonschema:"lines shown around each line listed: N, symbol or file"`
}

// argv collects command words, flags and positional arguments, and what
// the command reads on stdin. Flags take the --name=value form so values
// may start with a dash, and positional arguments follow "--" for the
// same reason.
type argv struct {
	words, flags, pos []string
	stdin             string
}

func (a *argv) flag(f string) { a.flags = append(a.flags, f) }

func (a *argv) str(name, v string) {
	if v != "" {
		a.flag("--" + name + "=" + v)
	}
}

func (a *argv) num(name string, v int) {
	if v != 0 {
		a.flag("--" + name + "=" + strconv.Itoa(v))
	}
}

func (a *argv) boolean(name string, v bool) {
	if v {
		a.flag("--" + name)
	}
}

func (a *argv) arg(v string) {
	if v != "" {
		a.pos = append(a.pos, v)
	}
}

func (a *argv) list() []string {
	out := append(append([]string{}, a.words...), a.flags...)
	if len(a.pos) > 0 {
		out = append(append(out, "--"), a.pos...)
	}
	return out
}

// required fails when a required input is empty.
func required(name, v, when string) error {
	if v != "" {
		return nil
	}
	if when != "" {
		return fmt.Errorf("input %s is required %s", name, when)
	}
	return fmt.Errorf("input %s is required", name)
}

// OverviewInput is the input of codedoc_overview.
type OverviewInput struct {
	Common
}

func (OverviewInput) build(a *argv) error {
	a.words = []string{"overview"}
	return nil
}

// OutlineInput is the input of codedoc_outline.
type OutlineInput struct {
	Common
	WithDetail
	Target string `json:"target" jsonschema:"file, document, directory or module (repo:path, a path in the current repo, a module identity or a dependency's package name)"`
}

func (in OutlineInput) build(a *argv) error {
	a.words = []string{"outline"}
	a.boolean("detail", in.Detail)
	a.arg(in.Target)
	return required("target", in.Target, "")
}

// LsInput is the input of codedoc_ls.
type LsInput struct {
	Common
	Paths []string `json:"paths,omitempty" jsonschema:"paths (repo:path or a path in the current repo) or globs; default: the server's directory, else every repo"`
	Since string   `json:"since,omitempty" jsonschema:"modified at or after: a duration (2d, 3h), a date or a git ref"`
	Sort  string   `json:"sort,omitempty" jsonschema:"sort key (default mtime, newest first)"`
	Long  bool     `json:"long,omitempty" jsonschema:"add size, kind, symbol count and title"`
}

func (in LsInput) build(a *argv) error {
	a.words = []string{"ls"}
	a.str("sort", in.Sort)
	a.str("since", in.Since)
	a.boolean("long", in.Long)
	for _, p := range in.Paths {
		a.arg(p)
	}
	return nil
}

// LinesInput is the input of codedoc_lines.
type LinesInput struct {
	Common
	Path  string `json:"path" jsonschema:"file (repo:path, or a path in the current repo)"`
	Range string `json:"range" jsonschema:"lines: A-B, A+N (A and the N lines after it) or A"`
}

func (in LinesInput) build(a *argv) error {
	a.words = []string{"lines"}
	a.arg(in.Path)
	a.arg(in.Range)
	if err := required("path", in.Path, ""); err != nil {
		return err
	}
	return required("range", in.Range, "")
}

// FindInput is the input of codedoc_find.
type FindInput struct {
	Common
	WithDetail
	Pattern string `json:"pattern" jsonschema:"symbol name, qualified name or glob"`
	Kind    string `json:"kind,omitempty" jsonschema:"symbol kind (function, method, class, ...); section or document match headings by substring"`
	Deps    bool   `json:"deps,omitempty" jsonschema:"include the third-party packages the workspace depends on"`
}

func (in FindInput) build(a *argv) error {
	a.words = []string{"find"}
	a.boolean("deps", in.Deps)
	a.boolean("detail", in.Detail)
	a.str("kind", in.Kind)
	a.arg(in.Pattern)
	return required("pattern", in.Pattern, "")
}

// SymbolInput is the input of codedoc_symbol: one address in symbol, or
// several in names, answered together under the one budget.
type SymbolInput struct {
	Common
	WithDetail
	Symbol string   `json:"symbol,omitempty" jsonschema:"address: a name (MemStore.Get), path#Name, path:LINE for the symbol enclosing a line, doc.md#Heading for a section of a document, or a stable key"`
	Names  []string `json:"names,omitempty" jsonschema:"several addresses to read in one call, instead of symbol"`
	NoBody bool     `json:"no_body,omitempty" jsonschema:"only location, signature and doc"`
	Deps   bool     `json:"deps,omitempty" jsonschema:"also look the name up in the third-party packages"`
}

func (in SymbolInput) build(a *argv) error {
	a.words = []string{"symbol"}
	a.boolean("deps", in.Deps)
	a.boolean("detail", in.Detail)
	a.boolean("no-body", in.NoBody)
	a.arg(in.Symbol)
	for _, n := range in.Names {
		a.arg(n)
	}
	if len(a.pos) > 0 {
		return nil
	}
	return required("symbol", in.Symbol, "unless names is set")
}

// GraphInput is the input of codedoc_graph.
type GraphInput struct {
	Common
	WithDetail
	WithLines
	Relation string `json:"relation" jsonschema:"the relation to list"`
	Target   string `json:"target" jsonschema:"symbol, type or module address (siblings also takes a file, a directory or config:KEY)"`
	Depth    int    `json:"depth,omitempty" jsonschema:"edges to follow (callers, callees, deps, dependents); above 1 the answer is a tree"`
	NoTests  bool   `json:"no_tests,omitempty" jsonschema:"leave out test code (callers, callees, refs, impls)"`
}

func (in GraphInput) build(a *argv) error {
	a.words = []string{in.Relation} // the schema restricts it to the enumeration
	a.num("depth", in.Depth)
	a.boolean("no-tests", in.NoTests)
	a.str("context", in.Context)
	a.boolean("detail", in.Detail)
	a.arg(in.Target)
	return required("target", in.Target, "")
}

// ConfigInput is the input of codedoc_config.
type ConfigInput struct {
	Common
	Key string `json:"key" jsonschema:"config key or glob (LIB_MODE, lib.*, *_MODE)"`
}

func (in ConfigInput) build(a *argv) error {
	a.words = []string{"config"}
	a.arg(in.Key)
	return required("key", in.Key, "")
}

// SearchInput is the input of codedoc_search.
type SearchInput struct {
	Common
	WithDetail
	WithLines
	Query string `json:"query" jsonschema:"what to look for, in words"`
	Kind  string `json:"kind,omitempty" jsonschema:"symbol kind (function, method, ..., section, document), or module"`
	Deps  bool   `json:"deps,omitempty" jsonschema:"include the third-party packages the workspace depends on"`
}

func (in SearchInput) build(a *argv) error {
	a.words = []string{"search"}
	a.boolean("deps", in.Deps)
	a.str("kind", in.Kind)
	a.str("context", in.Context)
	a.boolean("detail", in.Detail)
	a.arg(in.Query)
	return required("query", in.Query, "")
}

// WhereInput is the input of codedoc_where.
type WhereInput struct {
	Common
	Query   string `json:"query" jsonschema:"what is done, in words"`
	Limit   int    `json:"limit,omitempty" jsonschema:"locations shown (default 5)"`
	Context string `json:"context,omitempty" jsonschema:"lines around each match (default 2), or symbol for the whole symbol"`
	Deps    bool   `json:"deps,omitempty" jsonschema:"include the third-party packages the workspace depends on"`
}

func (in WhereInput) build(a *argv) error {
	a.words = []string{"where"}
	a.boolean("deps", in.Deps)
	a.num("limit", in.Limit)
	a.str("context", in.Context)
	a.arg(in.Query)
	return required("query", in.Query, "")
}

// ExplainInput is the input of codedoc_explain.
type ExplainInput struct {
	Common
	Target string `json:"target" jsonschema:"symbol address, module identity, path (repo:dir, repo:file) or a dependency's package name"`
}

func (in ExplainInput) build(a *argv) error {
	a.words = []string{"explain"}
	a.arg(in.Target)
	return required("target", in.Target, "")
}

// FlowInput is the input of codedoc_flow.
type FlowInput struct {
	Common
	Symbol string `json:"symbol" jsonschema:"entry symbol address"`
	Depth  int    `json:"depth,omitempty" jsonschema:"how many calls deep to go (default 3)"`
}

func (in FlowInput) build(a *argv) error {
	a.words = []string{"flow"}
	a.num("depth", in.Depth)
	a.arg(in.Symbol)
	return required("symbol", in.Symbol, "")
}

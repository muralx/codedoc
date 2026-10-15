package mcpserver

import (
	"context"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/muralx/codedoc/internal/query"
)

// enums lists the allowed values of the inputs that take one of a set,
// in every tool that has them (see toolEnums for one tool's own).
var enums = map[string][]string{
	"relation": {"callers", "callees", "refs", "impls", "supertypes", "tests", "siblings", "deps", "dependents"},
	"mode":     {"changes", "history", "at"},
	"sort":     {"mtime", "committed", "name", "size", "lines"},
}

// toolEnums lists, per tool, allowed values of inputs whose name other
// tools use with other values.
var toolEnums = map[string]map[string][]string{
	"codedoc_doc":    {"kind": {"missing", "stale", "module"}, "action": {"gaps", "set", "check"}},
	"codedoc_deps":   {"action": {"list", "index", "prune"}},
	"codedoc_impact": {"only": query.ImpactSections},
}

// registerTools registers the tools in the order a client lists them:
// one line each, a name, one sentence and an input type. A new tool is
// one more line (and its input type with a build method).
func registerTools(s *Server) {
	addTool[OverviewInput](s, "codedoc_overview",
		"Orient in the workspace: repos, README headings, entry points, recent commits, doc coverage and the module tree.")
	addTool[OutlineInput](s, "codedoc_outline",
		"List the symbols of a file, the headings of a document or the files of a directory.")
	addTool[LsInput](s, "codedoc_ls", "List indexed files, newest first, with date and line count.")
	addTool[LinesInput](s, "codedoc_lines", "Print a line range of a file under the symbols that enclose it.")
	addTool[FindInput](s, "codedoc_find", "Find symbols by name, qualified name or glob.")
	addTool[SymbolInput](s, "codedoc_symbol",
		"Read one symbol or document section, or several in one call: location, doc and source.")
	addTool[ImpactInput](s, "codedoc_impact",
		"Show what a change to a symbol touches: callers, references, implementations, tests, mentions in docs, config keys and exports.")
	addTool[HowInput](s, "codedoc_how", "Explain a symbol or a file and show the first two levels of its call tree.")
	addTool[APIInput](s, "codedoc_api",
		"Show the surface of a dependency: its modules and exported names, or one type with its members, the inherited ones included.")
	addTool[GraphInput](s, "codedoc_graph",
		"List one relation of a symbol or module: callers, callees, refs, impls, supertypes, tests, siblings, deps or dependents.")
	addTool[ConfigInput](s, "codedoc_config", "Show where a config key is defined, referenced and read by code.")
	addTool[SearchInput](s, "codedoc_search", "Search symbols, modules and document sections by meaning and keywords.")
	addTool[WhereInput](s, "codedoc_where",
		"Answer where something is done: the top locations for a description in words, each with the lines that match.")
	addTool[ExplainInput](s, "codedoc_explain",
		"Explain a symbol, module or file through its doc stack: repo, modules, container, doc, top callers and callees.")
	addTool[FlowInput](s, "codedoc_flow", "Show the call tree below a symbol.")
	addTool[HistoryInput](s, "codedoc_history",
		"Show what changed since a ref, the commits that touched a symbol, or a symbol at a ref.")
	addTool[DocInput](s, "codedoc_doc", "Rank documentation gaps, write docs into the source, or check doc states.")
	addTool[DepsInput](s, "codedoc_deps",
		"List the third-party packages of the workspace with their states, index them now, or prune unused versions.")
	addTool[FeedbackInput](s, "codedoc_feedback", "Report an answer that disagrees with the file, or list the reports.")
}

// addTool registers a tool whose input schema is inferred from In, with
// the enumerations above applied.
func addTool[In input](s *Server, name, desc string) {
	schema, err := jsonschema.For[In](nil)
	if err != nil {
		panic(fmt.Sprintf("tool %s: %v", name, err))
	}
	for _, set := range []map[string][]string{enums, toolEnums[name]} {
		for prop, values := range set {
			if p, ok := schema.Properties[prop]; ok {
				for _, v := range values {
					p.Enum = append(p.Enum, v)
				}
			}
		}
	}
	mcp.AddTool(s.mcp, &mcp.Tool{Name: name, Description: desc, InputSchema: schema},
		func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
			return s.call(ctx, name, in), nil, nil
		})
}

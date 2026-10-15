package query_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/query"
)

// sdkServerFile is a file of the fixture package @acme/sdk, relative to
// the repo deps-app.
const sdkServerFile = "node_modules/@acme/sdk/dist/server/index.d.ts"

// depFileForms returns the ways a file of a dependency is addressed:
// repo:path, relative to the current repo, and absolute.
func depFileForms(c *query.Ctx) []string {
	c.Repo = c.Workspace.Repos[0]
	return []string{"deps-app:" + sdkServerFile, sdkServerFile, filepath.Join(c.Repo.Path, sdkServerFile)}
}

func TestLinesOfADependencyFileListsItsSymbols(t *testing.T) {
	c := depsWorkspace(t)
	for _, addr := range depFileForms(c) {
		got := out(t, must(query.Lines(c, addr, "8-12")))
		wants(t, got, "/node_modules/@acme/sdk/dist/server/index.d.ts:8-12  in Server (8-14)"+
			"  [dep @acme/sdk@1.2.0]\n8\texport declare class Server extends Protocol {\n")
		if strings.Contains(got, "not indexed") || strings.Contains(got, "outside any indexed symbol") {
			t.Errorf("%s: a dependency file is indexed:\n%s", addr, got)
		}
	}
	got := out(t, must(query.Lines(c, "deps-app:"+sdkServerFile, "1-2")))
	wants(t, got, "index.d.ts:1-2  outside any indexed symbol  [dep @acme/sdk@1.2.0]\n1\timport ")
	// A range that is exactly one symbol is the lines symbol prints for
	// it, after its doc comment.
	got = out(t, must(query.Lines(c, "deps-app:"+sdkServerFile, "12")))
	wants(t, got, "index.d.ts:12  in Server.connect (12)  [dep @acme/sdk@1.2.0]\n12\t    connect(")
	wants(t, out(t, must(query.Symbol(c, sdkServer+"Server.connect", query.SymbolOpts{}))),
		"\n11\t    /** Attaches the server to a transport. */\n12\t    connect(transport: Transport): Promise<Result>\n")
}

func TestLinesOfAnUnindexedFileInAPackageDirectoryIsNotIndexed(t *testing.T) {
	c := depsWorkspace(t)
	got := out(t, must(query.Lines(c, "deps-app:node_modules/@acme/sdk/package.json", "1")))
	wants(t, got, "node_modules/@acme/sdk/package.json:1  outside any indexed symbol",
		"node_modules/@acme/sdk/package.json is not indexed: of the dependency @acme/sdk only the declarations its entry points export are indexed")
}

func TestSymbolAtALineOfADependencyFile(t *testing.T) {
	c := depsWorkspace(t)
	for _, addr := range depFileForms(c) {
		got := out(t, must(query.Symbol(c, addr+":9", query.SymbolOpts{})))
		wants(t, got, "Server  ", "index.d.ts:8-14  [dep @acme/sdk@1.2.0]\n5\t/**\n")
		// A line no symbol covers: the file is indexed, as for a
		// workspace file, and its symbols are the answer.
		resp, err := query.Symbol(c, addr+":1", query.SymbolOpts{})
		if !errors.Is(err, query.ErrNearest) {
			t.Fatalf("%s:1: err = %v", addr, err)
		}
		wants(t, render(t, resp), "== no exact match; nearest: (1)\nServer  dist/server/index.d.ts:8-14  ",
			"[dep @acme/sdk@1.2.0]", "no symbol encloses line 1 of dist/server/index.d.ts (14 lines)")
	}
	// Strictly it is not found, and the note points at the outline.
	c.Strict = true
	for _, addr := range depFileForms(c) {
		resp, err := query.Symbol(c, addr+":1", query.SymbolOpts{})
		if !errors.Is(err, query.ErrNotFound) || errors.Is(err, query.ErrNearest) {
			t.Fatalf("%s:1: err = %v", addr, err)
		}
		if !strings.Contains(resp.Note, "no symbol encloses line 1 of dist/server/index.d.ts (") ||
			!strings.Contains(resp.Note, " lines); codedoc outline ") || strings.Contains(resp.Note, "no indexed file") {
			t.Errorf("%s:1: note %q", addr, resp.Note)
		}
	}
	// The outline the note points at works as written.
	resp, _ := query.Symbol(c, "deps-app:"+sdkServerFile+":1", query.SymbolOpts{})
	hint := resp.Note[strings.Index(resp.Note, "codedoc outline ")+len("codedoc outline "):]
	hint = strings.Fields(hint)[0]
	wants(t, out(t, must(query.Outline(c, hint, query.OutlineOpts{}))), "== dist/server/index.d.ts [dep @acme/sdk@1.2.0], ")
}

func TestOutlineOfADependencyFileTakesEveryAddressForm(t *testing.T) {
	c := depsWorkspace(t)
	for _, addr := range depFileForms(c) {
		wants(t, out(t, must(query.Outline(c, addr, query.OutlineOpts{}))),
			"== dist/server/index.d.ts [dep @acme/sdk@1.2.0], 14 lines (1)\nServer  :8-14  export declare class Server")
	}
}

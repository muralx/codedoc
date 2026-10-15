package query_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/embed"
	"github.com/muralx/codedoc/internal/query"
)

// depsAndDocs indexes a repo with dependencies next to the repo of
// documents: the dependency index and the documents in one workspace.
func depsAndDocs(t *testing.T) *query.Ctx {
	t.Helper()
	return depsWorkspace(t, "deps-app", "doc-repo")
}

func TestSearchWithDependenciesKeepsSections(t *testing.T) {
	c := withDeps(depsAndDocs(t))
	c.Embedder = embed.NewFake(16)
	text := render(t, search(t, c, "exponentially notification", query.SearchOpts{}))
	wants(t, text, "\nNotes  doc-repo:docs/design.md:51-53  #### Notes  Retries back off exponentially.\n",
		"Sends a notification to the other side.  [dep @acme/sdk@1.2.0]", "  deps-app:src/")
	text = render(t, search(t, c, "exponentially notification", query.SearchOpts{Lang: "doc"}))
	if !strings.Contains(text, "#### Notes") || strings.Contains(text, "[dep ") {
		t.Errorf("--lang doc keeps dependencies out:\n%s", text)
	}
}

func TestSectionAndDependencyAddressesDoNotShadowOneAnother(t *testing.T) {
	c := depsAndDocs(t)
	c.Cwd = c.Workspace.Repos[0].Path
	c.Repo = c.Workspace.Repos[0]
	r, err := query.Symbol(c, "doc-repo:docs/design.md#6.2", query.SymbolOpts{})
	wants(t, text(t, r, err), "6.2 Retries  docs/design.md:46-53\n46\t### 6.2 Retries\n")
	wants(t, keyed(t, r), "[section]")
	r, err = query.Symbol(c, "node_modules/@acme/sdk/dist/shared/protocol.d.ts#Protocol.notification", query.SymbolOpts{})
	wants(t, text(t, r, err), "Protocol.notification  ")
	wants(t, keyed(t, r), sdkProtocol+"Protocol.notification", "[dep @acme/sdk@1.2.0]")
	r, err = query.Outline(c, "doc-repo:docs/design.md", query.OutlineOpts{})
	wants(t, text(t, r, err), "6.2 Retries")
	r, err = query.Outline(c, "@acme/sdk", query.OutlineOpts{Sort: "mtime", Reverse: true})
	wants(t, text(t, r, err), "== npm/@acme/sdk@1.2.0 modules (6)")
	r, err = query.Find(withDeps(c), "retries", query.FindOpts{Kind: "section"})
	if got := text(t, r, err); !strings.Contains(got, "== find retries (1)") || strings.Contains(got, "[dep ") {
		t.Errorf("a heading search finds no dependency:\n%s", got)
	}
}

func TestLineAddressInsideADependencyFile(t *testing.T) {
	c := depsAndDocs(t)
	c.Repo = c.Workspace.Repos[0]
	file := "node_modules/@acme/sdk/dist/shared/protocol.d.ts"
	r, err := query.Symbol(c, file+":9", query.SymbolOpts{})
	got := text(t, r, err)
	wants(t, got, "Protocol.notification  ", "[dep @acme/sdk@1.2.0]")
	wants(t, keyed(t, r), sdkProtocol+"Protocol.notification")
	if strings.Contains(got, "no indexed file") {
		t.Errorf("the workspace's note stays out:\n%s", got)
	}
	r, err = query.Symbol(c, "deps-app:"+file+":9", query.SymbolOpts{})
	wants(t, text(t, r, err), "Protocol.notification  ")
	r, err = query.Symbol(c, "src/main.ts:20", query.SymbolOpts{})
	wants(t, text(t, r, err), "App.announce  src/main.ts:19-21\n18\t  /** Tells the client that the application started. */\n")
	wants(t, keyed(t, r), depsAppKy+"App.announce")
	// A line no symbol encloses is answered with the symbols of the file.
	r, err = query.Symbol(c, file+":900", query.SymbolOpts{})
	if !errors.Is(err, query.ErrNearest) {
		t.Fatalf("a line past the end: %v", err)
	}
	wants(t, render(t, r), "== no exact match; nearest: (", "Protocol  ", "[dep @acme/sdk@1.2.0]",
		"no symbol encloses line 900 of dist/shared/protocol.d.ts (15 lines)")
	if r, err = query.Symbol(c, "node_modules/nope.d.ts:3", query.SymbolOpts{}); !errors.Is(err, query.ErrNotFound) || r != nil {
		t.Errorf("a file that does not exist: %v, %v", err, r)
	}
	c.Strict = true
	for addr, want := range map[string]string{file + ":900": "no symbol encloses line 900 of dist/shared/protocol.d.ts (15 lines)",
		"node_modules/nope.d.ts:3": "no indexed file"} {
		r, err = query.Symbol(c, addr, query.SymbolOpts{})
		if !errors.Is(err, query.ErrNotFound) || !strings.Contains(render(t, r), want) {
			t.Errorf("%s: %v\n%s", addr, err, render(t, r))
		}
	}
}

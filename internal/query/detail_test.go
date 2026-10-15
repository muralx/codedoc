package query_test

import (
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/query"
)

// --detail tags the symbols of find and of a file outline with their
// kind, fan-in and doc state; without it the answer is as before.
func TestDetailTagsTheSymbolsOfFindAndOutline(t *testing.T) {
	c := workspaceOf(t, "go-lib", "java-lib")
	c.Repo, c.Cwd = c.Workspace.Repos[0], c.Workspace.Repos[0].Path
	plain := run(t, must(query.Find(c, "Mem*", query.FindOpts{Kind: "struct"})).resp, nil)
	outline := run(t, must(query.Outline(c, "store/store.go", query.OutlineOpts{})).resp, nil)
	if strings.Contains(plain+outline, "[struct]") || strings.Contains(plain+outline, "fan-in") {
		t.Fatalf("detail without --detail:\n%s%s", plain, outline)
	}
	c.Detail = true
	got := run(t, must(query.Find(c, "Mem*", query.FindOpts{Kind: "struct"})).resp, nil)
	mustContain(t, got, "MemStore  store/store.go:18-20  type MemStore struct  MemStore is an in-memory Store.  [struct] [fan-in:4] [doc:ok]\n")
	got = run(t, must(query.Outline(c, "store/store.go", query.OutlineOpts{})).resp, nil)
	mustContain(t, got, "ErrMissing  :7  var ErrMissing = errors.New(\"missing\")  ErrMissing is returned when a key has no value.  [var] ",
		"Store  :10-15  type Store interface  Store persists string values by key.  [interface] [+2 members] [fan-in:",
		"MemStore.Get  :26-32  func (m *MemStore) Get(key string) (string, error)  Get implements Store.  [method] [fan-in:")
	got = run(t, must(query.Find(c, "NullStore.Get", query.FindOpts{})).resp, nil)
	mustContain(t, got, "  [method] [fan-in:0] [doc:missing]\n")
}

// An outline prints a signature as it is written: the kind is a tag of
// --detail, never a word before the signature.
func TestOutlinePrintsTheSignatureAsItIs(t *testing.T) {
	c := workspaceOf(t, "go-lib", "java-lib", "py-lib")
	got := run(t, must(query.Outline(c, "java-lib:src/main/java/com/fixture/store/Store.java", query.OutlineOpts{Doc: true})).resp, nil)
	mustContain(t, got, "Store  :8-19  public interface Store  Store is a key-value store.")
	got = run(t, must(query.Outline(c, "py-lib:src/pylib/store.py", query.OutlineOpts{})).resp, nil)
	mustContain(t, got, "DEFAULT_TIMEOUT  :9  DEFAULT_TIMEOUT = 30\n", "_missing  :47-48  def _missing(key)\n")
	// The member outline of a symbol over the budget likewise.
	c.Repo, c.Cwd, c.Budget = c.Workspace.Repos[2], c.Workspace.Repos[2].Path, 300
	got = run(t, must(query.Symbol(c, "src/pylib/store.py#MemStore", query.SymbolOpts{})).resp, nil)
	mustContain(t, got, "  MemStore.__init__  :32-35  def __init__(self)\n", "  MemStore.data  :33  self.data = {}\n")
	for _, word := range []string{"  method ", "  field ", "  interface public", "  const DEFAULT", "  function def"} {
		if strings.Contains(got, word) {
			t.Fatalf("the kind %q before a signature:\n%s", word, got)
		}
	}
}

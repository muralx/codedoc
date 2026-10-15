package query_test

import (
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/query"
)

func TestHistoryMapsUncommittedEditsToHead(t *testing.T) {
	c, _ := historyWorkspace(t)
	root := c.Workspace.Repos[0].Path
	filler := strings.Repeat("// filler line\n", 40)
	editRepo(t, root, "store/store.go", "import \"errors\"\n", "import \"errors\"\n\n"+filler)
	r, err := query.History(c, libStore+"MemStore.Get", 0)
	text := run(t, r, err)
	mustContain(t, text, "== history of MemStore.Get (2)", "2026-02-01 Tess  get: copy the value",
		"2026-01-01 fixture  fixture: initial")
	r, err = query.History(c, libStore+"Unused", 0)
	mustContain(t, run(t, r, err), libStore+"Unused does not exist at HEAD; its history starts in the working tree")
}

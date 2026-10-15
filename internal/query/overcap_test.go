package query_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/query"
)

func TestCallersNoteOverCapReferences(t *testing.T) {
	var b strings.Builder
	b.WriteString("package a\n\nfunc use(x X) { x.Run() }\n")
	for i := 0; i < 21; i++ {
		fmt.Fprintf(&b, "\ntype T%d struct{}\n\nfunc (T%d) Run() {}\n", i, i)
	}
	c := tempWorkspace(t, map[string]string{"go.mod": "module example.com/a\n", "a.go": b.String()})
	r, err := query.Callers(c, "T3.Run", query.GraphOpts{Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if want := "1 reference named Run had more than 20 candidates and is not listed"; !strings.Contains(r.Note, want) {
		t.Fatalf("note %q", r.Note)
	}
	r, err = query.Callers(c, "use", query.GraphOpts{Depth: 1})
	if err != nil || r.Note != "" {
		t.Fatalf("%q %v", r.Note, err)
	}
}

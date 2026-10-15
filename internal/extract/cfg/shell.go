package cfg

import (
	"fmt"
	"strings"

	"github.com/muralx/codedoc/internal/parse"
)

// shellExtractor reads shell scripts with tree-sitter-bash: assignments
// (X=..., export X=..., local X=...) define X; $X and ${X} expansions refer
// to X. Positional and special parameters ($1, $@, $?) are skipped.
type shellExtractor struct{}

// Extract implements Extractor. The bash grammar recovers from syntax
// errors, so only a parser failure is an error.
func (shellExtractor) Extract(_ string, src []byte) (*Result, error) {
	tree, err := parse.Parse(parse.Bash, src)
	if err != nil {
		return nil, fmt.Errorf("shell: %w", err)
	}
	defer tree.Close()
	r := &Result{Format: Shell}
	walkShell(r, src, tree.Root())
	return r, nil
}

// walkShell visits n and its descendants in source order.
func walkShell(r *Result, src []byte, n parse.Node) {
	switch n.Kind() {
	case "variable_assignment":
		shellAssignment(r, src, n)
	case "simple_expansion", "expansion":
		shellExpansion(r, src, n)
	}
	for i := uint(0); i < n.NamedChildCount(); i++ {
		walkShell(r, src, n.NamedChild(i))
	}
}

// shellAssignment defines the assigned variable; array element
// assignments (A[0]=x) are skipped.
func shellAssignment(r *Result, src []byte, n parse.Node) {
	name := n.ChildByField("name")
	if name == nil || name.Kind() != "variable_name" {
		return
	}
	value := ""
	if v := n.ChildByField("value"); v != nil {
		value = unquote(v.Text(src))
	}
	r.addDef(name.Text(src), int(n.StartRow())+1, value)
}

// shellExpansion records the variable an expansion names, unless it is a
// special or positional parameter.
func shellExpansion(r *Result, src []byte, n parse.Node) {
	for i := uint(0); i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		if c.Kind() != "variable_name" {
			continue
		}
		if name := c.Text(src); name[0] < '0' || name[0] > '9' {
			r.addRef(name, int(c.StartRow())+1)
		}
		return
	}
}

// unquote strips one pair of matching surrounding quotes.
func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && strings.HasSuffix(s, s[:1]) {
		return s[1 : len(s)-1]
	}
	return s
}

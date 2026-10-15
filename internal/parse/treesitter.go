package parse

import (
	"fmt"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// Parse parses src as the given precise language. A parser is created per
// call so there is no shared mutable state; the caller must Close the tree.
func Parse(lang string, src []byte) (Tree, error) {
	grammar, ok := grammars[lang]
	if !ok {
		return nil, fmt.Errorf("parse: unknown language %q", lang)
	}
	p := sitter.NewParser()
	defer p.Close()
	if err := p.SetLanguage(grammar); err != nil {
		return nil, fmt.Errorf("parse: set language %s: %w", lang, err)
	}
	t := p.Parse(src, nil)
	if t == nil {
		return nil, fmt.Errorf("parse: %s parser returned no tree", lang)
	}
	return &tree{t: t}, nil
}

type tree struct{ t *sitter.Tree }

func (t *tree) Root() Node { return wrap(t.t.RootNode()) }
func (t *tree) Close()     { t.t.Close() }

type node struct{ n *sitter.Node }

// wrap converts a possibly nil binding node into a Node, returning an
// untyped nil interface for a missing node.
func wrap(n *sitter.Node) Node {
	if n == nil {
		return nil
	}
	return node{n: n}
}

func (n node) Kind() string                  { return n.n.Kind() }
func (n node) IsNamed() bool                 { return n.n.IsNamed() }
func (n node) StartByte() uint               { return n.n.StartByte() }
func (n node) EndByte() uint                 { return n.n.EndByte() }
func (n node) StartRow() uint                { return n.n.StartPosition().Row }
func (n node) StartColumn() uint             { return n.n.StartPosition().Column }
func (n node) EndRow() uint                  { return n.n.EndPosition().Row }
func (n node) ChildCount() uint              { return n.n.ChildCount() }
func (n node) Child(i uint) Node             { return wrap(n.n.Child(i)) }
func (n node) NamedChildCount() uint         { return n.n.NamedChildCount() }
func (n node) NamedChild(i uint) Node        { return wrap(n.n.NamedChild(i)) }
func (n node) ChildByField(name string) Node { return wrap(n.n.ChildByFieldName(name)) }
func (n node) Parent() Node                  { return wrap(n.n.Parent()) }
func (n node) PrevSibling() Node             { return wrap(n.n.PrevSibling()) }
func (n node) NextSibling() Node             { return wrap(n.n.NextSibling()) }
func (n node) Text(src []byte) string        { return n.n.Utf8Text(src) }
func (n node) HasError() bool                { return n.n.HasError() }

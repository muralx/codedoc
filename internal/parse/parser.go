// Package parse wraps tree-sitter behind a narrow interface so extractors do
// not depend on the CGO binding directly.
package parse

// Tree is a parsed file. Close must be called; nodes obtained from the tree
// are invalid after Close.
type Tree interface {
	Root() Node
	Close()
}

// Node is a syntax node. Positions are 0-based rows; callers add 1 for
// lines. Navigation methods return a nil interface when the node is absent.
type Node interface {
	Kind() string
	IsNamed() bool
	StartByte() uint
	EndByte() uint
	StartRow() uint
	// StartColumn is the 0-based byte offset of the node's start in its
	// row.
	StartColumn() uint
	EndRow() uint
	ChildCount() uint
	Child(i uint) Node
	NamedChildCount() uint
	NamedChild(i uint) Node
	ChildByField(name string) Node
	Parent() Node
	PrevSibling() Node
	NextSibling() Node
	Text(src []byte) string
	HasError() bool
}

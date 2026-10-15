package cfg

import (
	"bytes"
	"errors"
	"io"
	"strconv"

	"gopkg.in/yaml.v3"
)

// yamlExtractor defines every scalar leaf as the dotted path of its mapping
// keys, with sequence items by index, in every document of the stream.
type yamlExtractor struct{}

// Extract implements Extractor.
func (yamlExtractor) Extract(_ string, src []byte) (*Result, error) {
	r := &Result{Format: YAML}
	dec := yaml.NewDecoder(bytes.NewReader(src))
	for {
		var doc yaml.Node
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return r, nil
		}
		if err != nil {
			return r, err
		}
		for _, n := range doc.Content {
			walkYAML(r, "", n, n.Line)
		}
	}
}

// maxExpansion bounds nested alias and merge expansions.
const maxExpansion = 8

// yamlWalk records the leaves of a YAML tree. Outside an expansion (at
// is 0) a leaf is defined on the line of its key or item. Inside the
// expansion of an alias or a merge key every leaf is defined at the line
// that uses the anchor, and placeholders are not recorded again.
type yamlWalk struct {
	r     *Result
	at    int
	depth int
}

// walkYAML records the leaves under n, whose dotted path is prefix; line is
// where the leaf is defined (the line of its mapping key, or of the item).
func walkYAML(r *Result, prefix string, n *yaml.Node, line int) {
	yamlWalk{r: r}.node(prefix, n, line)
}

func (y yamlWalk) node(prefix string, n *yaml.Node, line int) {
	switch n.Kind {
	case yaml.MappingNode:
		y.mapping(prefix, n, line)
	case yaml.SequenceNode:
		if len(n.Content) == 0 {
			defineLeaf(y.r, prefix, line, "[]")
		}
		for i, item := range n.Content {
			y.node(join(prefix, strconv.Itoa(i)), item, y.line(item.Line))
		}
	case yaml.AliasNode:
		if n.Alias != nil && n.Alias.Kind == yaml.MappingNode && y.depth < maxExpansion {
			y.expand(line).node(prefix, n.Alias, line)
			return
		}
		defineLeaf(y.r, prefix, line, "*"+n.Value)
	case yaml.ScalarNode:
		defineLeaf(y.r, prefix, line, n.Value)
		if y.at == 0 {
			y.r.addPlaceholders(n.Value, scalarStart(n))
		}
	}
}

// line returns where a leaf found at line is defined.
func (y yamlWalk) line(line int) int {
	if y.at != 0 {
		return y.at
	}
	return line
}

// expand returns the walker for an expansion used at line.
func (y yamlWalk) expand(line int) yamlWalk {
	return yamlWalk{r: y.r, at: line, depth: y.depth + 1}
}

// mapping walks the key and value pairs of a mapping node. Merge keys (<<)
// bring in the keys of the mappings they name that the mapping does not
// set itself; among several merged mappings the first wins.
func (y yamlWalk) mapping(prefix string, n *yaml.Node, line int) {
	if len(n.Content) == 0 {
		defineLeaf(y.r, prefix, line, "{}")
	}
	own := map[string]bool{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if !isMergeKey(n.Content[i]) {
			own[n.Content[i].Value] = true
		}
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if isMergeKey(k) {
			y.merge(prefix, v, y.line(k.Line), own)
			continue
		}
		y.node(join(prefix, k.Value), v, y.line(k.Line))
	}
}

// merge defines under prefix, at line, the keys of the mappings that the
// value of a merge key names (an alias, a list of aliases or an inline
// mapping), skipping the keys in seen and adding the ones it defines.
func (y yamlWalk) merge(prefix string, v *yaml.Node, line int, seen map[string]bool) {
	if y.depth >= maxExpansion {
		return
	}
	switch v.Kind {
	case yaml.AliasNode:
		if v.Alias != nil {
			y.merge(prefix, v.Alias, line, seen)
		}
	case yaml.SequenceNode:
		for _, item := range v.Content {
			y.merge(prefix, item, line, seen)
		}
	case yaml.MappingNode:
		sub := y.expand(line)
		for i := 0; i+1 < len(v.Content); i += 2 {
			k, val := v.Content[i], v.Content[i+1]
			switch {
			case isMergeKey(k):
				sub.merge(prefix, val, line, seen)
			case !seen[k.Value]:
				seen[k.Value] = true
				sub.node(join(prefix, k.Value), val, line)
			}
		}
	}
}

// isMergeKey reports whether k is the YAML merge key <<.
func isMergeKey(k *yaml.Node) bool { return k.Value == "<<" && k.Tag == "!!merge" }

// defineLeaf records a leaf definition unless it has no path (a document
// that is a bare scalar).
func defineLeaf(r *Result, key string, line int, value string) {
	if key != "" {
		r.addDef(key, line, value)
	}
}

// join appends a segment to a dotted path.
func join(prefix, seg string) string {
	if prefix == "" {
		return seg
	}
	return prefix + "." + seg
}

// scalarStart returns the line of a scalar's first content line: block
// scalars (| and >) start on the line after their indicator.
func scalarStart(n *yaml.Node) int {
	if n.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
		return n.Line + 1
	}
	return n.Line
}

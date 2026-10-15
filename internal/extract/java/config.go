package java

import (
	"strings"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/parse"
)

// configArgs records config key candidates among the string literal
// arguments of a call or instantiation (spec 7.2): the first argument of
// an accessor such as System.getenv, and any argument that looks like a
// key.
func (r *refWalker) configArgs(n parse.Node) {
	args := n.ChildByField("arguments")
	if args == nil {
		return
	}
	accessor := false
	if name := n.ChildByField("name"); name != nil {
		accessor = extract.IsConfigAccessor("java", r.qualifier(n.ChildByField("object")), name.Text(r.src))
	}
	for i := uint(0); i < args.NamedChildCount(); i++ {
		arg := args.NamedChild(i)
		if accessor && i == 0 {
			r.configConst(arg)
		}
		if key, ok := r.literal(arg); ok && ((accessor && i == 0) || extract.LooksLikeKey(key)) {
			r.config(key, arg, accessor && i == 0)
		}
	}
}

// configConst records an accessor's key argument written as a name
// (KEY, Class.KEY or this.KEY) as a config candidate read through that
// constant.
func (r *refWalker) configConst(arg parse.Node) {
	name := arg
	if arg.Kind() == "field_access" {
		name = arg.ChildByField("field")
	} else if arg.Kind() != "identifier" {
		return
	}
	if name != nil {
		r.res.References = append(r.res.References, extract.ConfigConstRef(name.Text(r.src),
			r.enc.At(name.StartByte()), extract.Line(name), extract.Col(name)))
	}
}

// valuePlaceholders records the keys of the ${key} and ${key:default}
// placeholders in the string arguments of a Spring @Value annotation
// named name.
func (r *refWalker) valuePlaceholders(name, args parse.Node) {
	if r.lastSegment(name) != "Value" {
		return
	}
	for i := uint(0); i < args.NamedChildCount(); i++ {
		arg := args.NamedChild(i)
		if arg.Kind() == "element_value_pair" {
			arg = arg.ChildByField("value")
		}
		if text, ok := r.literal(arg); ok {
			for _, key := range extract.PlaceholderKeys(text) {
				r.config(key, arg, true)
			}
		}
	}
}

// config records key at node at as a config candidate, read through an
// accessor or only a key-like literal.
func (r *refWalker) config(key string, at parse.Node, accessor bool) {
	r.res.References = append(r.res.References,
		extract.ConfigRef(key, accessor, r.enc.At(at.StartByte()), extract.Line(at), extract.Col(at)))
}

// literal returns the text of a non-empty string literal without escape
// sequences.
func (w *walker) literal(n parse.Node) (string, bool) {
	if n == nil || n.Kind() != "string_literal" || n.NamedChildCount() == 0 {
		return "", false
	}
	var b strings.Builder
	for i := uint(0); i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		if c.Kind() != "string_fragment" {
			return "", false
		}
		b.WriteString(c.Text(w.src))
	}
	return b.String(), true
}

package python

import (
	"strings"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/parse"
)

// configArgs records config key candidates among the string literal
// arguments of a call (spec 7.2): the first argument of an accessor such
// as os.getenv, and any argument, keyword values included, that looks
// like a key.
func (r *refWalker) configArgs(name, qualifier parse.Node, args parse.Node) {
	if args == nil || args.Kind() != "argument_list" {
		return
	}
	q := ""
	if qualifier != nil {
		q = chains.Qualifier(qualifier, r.src)
	}
	accessor := extract.IsConfigAccessor("py", q, name.Text(r.src))
	pos := 0
	for i := uint(0); i < args.NamedChildCount(); i++ {
		if arg := args.NamedChild(i); arg.Kind() != "comment" {
			r.configArg(arg, accessor && pos == 0)
			pos++
		}
	}
}

// configArg records one call argument as a config key candidate: the key
// of an accessor (key set: its first argument), a string or, unless
// passed by keyword, a constant name; or else a key-like string, keyword
// values included.
func (r *refWalker) configArg(arg parse.Node, key bool) {
	if arg.Kind() == "keyword_argument" {
		arg = arg.ChildByField("value")
	} else if key {
		r.configConst(arg)
	}
	if text, ok := r.literal(arg); ok && (key || extract.LooksLikeKey(text)) {
		r.config(text, arg, key)
	}
}

// configSubscript records os.environ["X"] as a config key candidate.
func (r *refWalker) configSubscript(n parse.Node) {
	value := n.ChildByField("value")
	if value == nil || !extract.IsConfigMap("py", chains.Qualifier(value, r.src)) {
		return
	}
	key := n.ChildByField("subscript")
	if text, ok := r.literal(key); ok {
		r.config(text, n, true)
	} else if key != nil {
		r.configConst(key)
	}
}

// configConst records an accessor's key argument written as a name (KEY
// or mod.KEY) as a config candidate read through that constant.
func (r *refWalker) configConst(arg parse.Node) {
	name := arg
	if arg.Kind() == "attribute" {
		name = arg.ChildByField("attribute")
	} else if arg.Kind() != "identifier" {
		return
	}
	if name != nil {
		r.res.References = append(r.res.References, extract.ConfigConstRef(name.Text(r.src),
			r.enc.At(name.StartByte()), extract.Line(name), extract.Col(name)))
	}
}

// config records key at node at as a config candidate, read through an
// accessor or only a key-like literal.
func (r *refWalker) config(key string, at parse.Node, accessor bool) {
	r.res.References = append(r.res.References,
		extract.ConfigRef(key, accessor, r.enc.At(at.StartByte()), extract.Line(at), extract.Col(at)))
}

// literal returns the text of a plain string literal: no f-string or
// bytes prefix, no interpolation. Escape sequences are kept as written.
func (r *refWalker) literal(n parse.Node) (string, bool) { return stringLiteral(n, r.src) }

// stringLiteral returns the text of a plain string literal; see literal.
func stringLiteral(n parse.Node, src []byte) (string, bool) {
	if n == nil || n.Kind() != "string" || n.NamedChildCount() < 2 {
		return "", false
	}
	start, end := n.NamedChild(0), n.NamedChild(n.NamedChildCount()-1)
	if start.Kind() != "string_start" || end.Kind() != "string_end" || strings.ContainsAny(start.Text(src), "fFbB") {
		return "", false
	}
	for i := uint(1); i+1 < n.NamedChildCount(); i++ {
		if n.NamedChild(i).Kind() == "interpolation" {
			return "", false
		}
	}
	return string(src[start.EndByte():end.StartByte()]), true
}

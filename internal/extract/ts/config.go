package ts

import (
	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/parse"
)

// envAccess records process.env.X and process.env["X"] as config key
// candidates (spec 7.2).
func (r *refWalker) envAccess(n parse.Node) {
	obj := n.ChildByField("object")
	if obj == nil || !extract.IsConfigMap("ts", chains.Qualifier(obj, r.src)) {
		return
	}
	key := n.ChildByField("property")
	if n.Kind() == "subscript_expression" {
		key = n.ChildByField("index")
		if text, ok := r.literal(key); ok {
			r.config(text, key, true)
		} else if key != nil {
			r.configConst(key)
		}
		return
	}
	if key != nil {
		r.config(key.Text(r.src), key, true)
	}
}

// configConst records a process.env subscript written as a name (KEY,
// Cfg.KEY or this.KEY) as a config candidate read through that
// constant, and a member name as a field reference, which tells the
// resolver the qualifier the constant is reached through.
func (r *refWalker) configConst(key parse.Node) {
	name := key
	switch key.Kind() {
	case "member_expression":
		name = key.ChildByField("property")
		r.add(extract.RefField, name, key.ChildByField("object"), -1)
	case "identifier":
	default:
		return
	}
	if name != nil {
		r.res.References = append(r.res.References, extract.ConfigConstRef(name.Text(r.src),
			r.enc.At(name.StartByte()), extract.Line(name), extract.Col(name)))
	}
}

// configArgs records the string literal arguments of a call that look
// like config keys.
func (r *refWalker) configArgs(args parse.Node) {
	for i := uint(0); args != nil && i < args.NamedChildCount(); i++ {
		arg := args.NamedChild(i)
		if key, ok := r.literal(arg); ok && extract.LooksLikeKey(key) {
			r.config(key, arg, false)
		}
	}
}

// config records key at node at as a config candidate, read through an
// accessor or only a key-like literal.
func (r *refWalker) config(key string, at parse.Node, accessor bool) {
	r.res.References = append(r.res.References,
		extract.ConfigRef(key, accessor, r.enc.At(at.StartByte()), extract.Line(at), extract.Col(at)))
}

// literal returns the text of a string literal without escape sequences
// or of a template string without substitutions.
func (r *refWalker) literal(n parse.Node) (string, bool) { return stringLiteral(n, r.src) }

// stringLiteral returns the text of a string literal; see literal.
func stringLiteral(n parse.Node, src []byte) (string, bool) {
	if n == nil || (n.Kind() != "string" && n.Kind() != "template_string") || n.NamedChildCount() == 0 {
		return "", false
	}
	text := ""
	for i := uint(0); i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		if c.Kind() != "string_fragment" {
			return "", false
		}
		text += c.Text(src)
	}
	return text, true
}

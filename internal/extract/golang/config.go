package golang

import (
	"strconv"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/parse"
)

// configArgs records config key candidates among the string literal
// arguments of a call (spec 7.2): the first argument of an accessor such
// as os.Getenv or viper.GetString, and any argument that looks like a key.
func (r *refWalker) configArgs(call, name, qualifier parse.Node) {
	args := call.ChildByField("arguments")
	if args == nil {
		return
	}
	q := ""
	if qualifier != nil {
		q = chains.Qualifier(qualifier, r.src)
	}
	accessor := name != nil && extract.IsConfigAccessor("go", q, name.Text(r.src))
	for i := uint(0); i < args.NamedChildCount(); i++ {
		arg := args.NamedChild(i)
		if accessor && i == 0 {
			r.configConst(arg)
		}
		if key, ok := r.literal(arg); ok && ((accessor && i == 0) || extract.LooksLikeKey(key)) {
			r.res.References = append(r.res.References, extract.ConfigRef(key, accessor && i == 0,
				r.enc.At(arg.StartByte()), extract.Line(arg), extract.Col(arg)))
		}
	}
}

// configConst records an accessor's key argument written as a name
// (KEY or pkg.KEY) as a config candidate read through that constant.
func (r *refWalker) configConst(arg parse.Node) {
	name := arg
	if arg.Kind() == "selector_expression" {
		name = arg.ChildByField("field")
	} else if arg.Kind() != "identifier" {
		return
	}
	if name != nil {
		r.res.References = append(r.res.References, extract.ConfigConstRef(name.Text(r.src),
			r.enc.At(name.StartByte()), extract.Line(name), extract.Col(name)))
	}
}

// literal returns the value of a non-empty interpreted or raw string
// literal.
func (r *refWalker) literal(n parse.Node) (string, bool) { return stringValue(n, r.src) }

// stringValue returns the value of a non-empty interpreted or raw string
// literal.
func stringValue(n parse.Node, src []byte) (string, bool) {
	if n == nil || (n.Kind() != "interpreted_string_literal" && n.Kind() != "raw_string_literal") {
		return "", false
	}
	s, err := strconv.Unquote(n.Text(src))
	return s, err == nil && s != ""
}

// specValues returns the string literal values of a const spec by
// position of the names it declares: "" where the value is no string
// literal or missing (iota continuation).
func specValues(spec parse.Node, src []byte) []string {
	list := spec.ChildByField("value")
	if list == nil {
		return nil
	}
	out := make([]string, list.NamedChildCount())
	for i := range out {
		out[i], _ = stringValue(list.NamedChild(uint(i)), src)
	}
	return out
}

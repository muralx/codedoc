package ts

import (
	"slices"
	"strings"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/parse"
)

// testRef remembers where a test symbol came from, to qualify repeated
// titles once the whole file is known.
type testRef struct {
	index     int      // position in res.Symbols
	key       string   // <callee>:<title>
	describes []string // titles of the enclosing describe blocks, outermost first
}

// testCalls adds a synthetic test function for every it(...) or test(...)
// call with a literal title found under n, including inside describe
// blocks; describes holds the titles of the enclosing describe blocks.
func (w *walker) testCalls(n parse.Node, describes []string) {
	if n.Kind() == "call_expression" {
		callee, title, ok := w.testCall(n)
		switch {
		case ok && callee == "describe":
			describes = append(slices.Clip(describes), title)
		case ok:
			s := w.symbol(extract.KindFunction, title, "", single(n), n, extract.Package)
			s.Qualified = w.prefix + callee + ":" + title
			s.IsTest = true
			w.tests = append(w.tests, testRef{index: len(w.res.Symbols), key: callee + ":" + title, describes: describes})
			w.add(s)
			return
		}
	}
	for i := uint(0); i < n.NamedChildCount(); i++ {
		w.testCalls(n.NamedChild(i), describes)
	}
}

// qualifyTests puts the describe path into the qualified name of every
// test whose callee and title repeat in the file:
// <path>/describe:<outer>/describe:<inner>/it:<title>.
func (w *walker) qualifyTests() {
	count := map[string]int{}
	for _, t := range w.tests {
		count[t.key]++
	}
	for _, t := range w.tests {
		if count[t.key] < 2 || len(t.describes) == 0 {
			continue
		}
		var b strings.Builder
		for _, d := range t.describes {
			b.WriteString("describe:" + d + "/")
		}
		w.res.Symbols[t.index].Qualified = w.prefix + b.String() + t.key
	}
}

// testCall reports whether call is a test or describe call with a literal
// title: it, test or describe, optionally as .only(...), .skip(...) or
// .each(table)(...). It returns the base callee and the unquoted title.
func (w *walker) testCall(call parse.Node) (callee, title string, ok bool) {
	fn, args := call.ChildByField("function"), call.ChildByField("arguments")
	if fn == nil || args == nil || args.NamedChildCount() == 0 {
		return "", "", false
	}
	callee = w.testCallee(fn)
	first := args.NamedChild(0)
	if callee == "" || (first.Kind() != "string" && first.Kind() != "template_string") {
		return "", "", false
	}
	text := first.Text(w.src)
	return callee, text[1 : len(text)-1], true
}

// testCallee returns "it", "test" or "describe" when fn calls one of them,
// directly, through .only or .skip, or as the result of .each(table).
func (w *walker) testCallee(fn parse.Node) string {
	each := fn.Kind() == "call_expression"
	if each {
		fn = fn.ChildByField("function")
	}
	base := w.testBase(fn, each)
	if base == nil || base.Kind() != "identifier" {
		return ""
	}
	switch name := base.Text(w.src); name {
	case "it", "test", "describe":
		return name
	}
	return ""
}

// testBase returns the object of fn when fn is x.only or x.skip (or x.each
// when each is set), fn itself when it is a plain name and each is not set,
// and nil otherwise.
func (w *walker) testBase(fn parse.Node, each bool) parse.Node {
	if fn == nil || fn.Kind() != "member_expression" {
		if each {
			return nil
		}
		return fn
	}
	prop := fn.ChildByField("property")
	if prop == nil {
		return nil
	}
	modifier := prop.Text(w.src)
	if each != (modifier == "each") || (modifier != "each" && modifier != "only" && modifier != "skip") {
		return nil
	}
	return fn.ChildByField("object")
}

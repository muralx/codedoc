package extract_test

import (
	"reflect"
	"testing"

	"github.com/muralx/codedoc/internal/extract"
)

func TestTypePathsAreBuiltFromKnownTypes(t *testing.T) {
	for _, tc := range []struct{ got, want string }{
		{extract.FieldOf("Options", "client"), "Options#client"},
		{extract.FieldOf(extract.FieldOf("Options", "a"), "b"), "Options#a#b"},
		{extract.FieldOf("", "client"), ""},
		{extract.FieldOf(extract.UnknownType, "client"), ""},
		{extract.FieldOf(extract.BuiltinType, "client"), ""},
		{extract.FieldOf("[]Item", "client"), ""},
		{extract.FieldOf("Options", ""), ""},
		{extract.ResultOf("Store", "get"), "Store#get()"},
		{extract.ResultOf("", "get"), ""},
		{extract.ArrayOf("Item"), "[]Item"},
		{extract.ArrayOf(extract.UnknownType), "[]"},
		{extract.ElemOf("[]Item"), "Item"},
		{extract.ElemOf("[]"), ""},
		{extract.ElemOf("Options#items"), "Options#items@"},
		{extract.ElemOf(extract.BuiltinType), ""},
		{extract.Awaited("Store#get()"), "Store#get()^"},
		{extract.Awaited("[]Item"), "[]Item"},
		{extract.Awaited(extract.BuiltinType), ""},
		{extract.Awaited(""), ""},
	} {
		if tc.got != tc.want {
			t.Errorf("got %q, want %q", tc.got, tc.want)
		}
	}
}

func TestParseTypePath(t *testing.T) {
	field := func(n string) extract.TypeStep { return extract.TypeStep{Kind: extract.StepField, Name: n} }
	result := func(n string) extract.TypeStep { return extract.TypeStep{Kind: extract.StepResult, Name: n} }
	elem, await := extract.TypeStep{Kind: extract.StepElem}, extract.TypeStep{Kind: extract.StepAwait}
	for path, want := range map[string]struct {
		root  string
		steps []extract.TypeStep
	}{
		"Options":               {"Options", nil},
		"kit.New()":             {"kit.New()", nil},
		"Options#client":        {"Options", []extract.TypeStep{field("client")}},
		"this#store#get()^":     {"this", []extract.TypeStep{field("store"), result("get"), await}},
		"$s#opts#Items@":        {"$s", []extract.TypeStep{field("opts"), field("Items"), elem}},
		"load()^@#name":         {"load()", []extract.TypeStep{await, elem, field("name")}},
		"ns.Store#all()@#touch": {"ns.Store", []extract.TypeStep{result("all"), elem, field("touch")}},
	} {
		root, steps := extract.ParseTypePath(path)
		if root != want.root || !reflect.DeepEqual(steps, want.steps) {
			t.Errorf("%s: %q %v, want %q %v", path, root, steps, want.root, want.steps)
		}
	}
}

func TestLocalsTellInnerScopesAndProperties(t *testing.T) {
	l := extract.NewLocals()
	l.Declare("top", "T")
	l.Declare("list", "[]Item")
	if l.Nested() || l.Inner("top") {
		t.Error("the file scope is no inner scope")
	}
	l.Push()
	l.Declare("x", "X")
	if !l.Nested() || !l.Inner("x") || l.Inner("top") {
		t.Errorf("nested %v, inner x %v, inner top %v", l.Nested(), l.Inner("x"), l.Inner("top"))
	}
	if got := l.TypeOf("f", "list.push"); got != extract.BuiltinType {
		t.Errorf("an array's own members are builtin: %q", got)
	}
	l.DeclareProperty("src/a.ts/C.m", "items", "[]Item")
	if got := l.Property("src/a.ts/C.other", "items"); got != "[]Item" {
		t.Errorf("property: %q", got)
	}
	if got := l.TypeOf("src/a.ts/C.other", "this.items"); got != extract.BuiltinType {
		t.Errorf("this.items: %q", got)
	}
}

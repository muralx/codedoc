package extract_test

import (
	"testing"

	"github.com/muralx/codedoc/internal/extract"
)

func TestLocalsLookUpVariablesThroughScopes(t *testing.T) {
	l := extract.NewLocals()
	l.Declare("top", "Store")
	l.Push()
	l.Declare("srv", "Server")
	l.Declare("n", extract.BuiltinType)
	l.Declare("_", "T")
	l.Declare("loose", "")
	for _, c := range []struct{ qualifier, want string }{
		{"srv", "Server"}, {"srv.inner.x", "Server"}, {"n", "-"}, {"top.x", "Store"}, {"loose", extract.UnknownType},
		{"_", ""}, {"other", ""}, {"srv()", ""}, {"", ""},
	} {
		if got := l.TypeOf("run", c.qualifier); got != c.want {
			t.Errorf("TypeOf(%q) = %q, want %q", c.qualifier, got, c.want)
		}
	}
	l.Push()
	l.Declare("srv", "")
	if typ, ok := l.Lookup("srv"); typ != "" || !ok {
		t.Errorf("an inner declaration without a type hides the outer one: %q %v", typ, ok)
	}
	l.Pop()
	if typ, _ := l.Lookup("srv"); typ != "Server" {
		t.Errorf("leaving a scope brings the outer variable back: %q", typ)
	}
	l.Pop()
	if _, ok := l.Lookup("srv"); ok {
		t.Error("a variable outlived its scope")
	}
	l.Pop()
	if typ, ok := l.Lookup("top"); typ != "Store" || !ok {
		t.Error("the file scope is never popped")
	}
}

func TestLocalsRetypeTheNearestDeclaration(t *testing.T) {
	l := extract.NewLocals()
	l.Declare("x", "A")
	l.Push()
	if !l.Retype("x", "") || l.Retype("missing", "B") {
		t.Error("Retype reports whether the name is declared")
	}
	l.Pop()
	if got := l.TypeOf("f", "x"); got != extract.UnknownType {
		t.Errorf("a retyped variable keeps its new type after the scope: %q", got)
	}
}

func TestLocalsKeepPropertiesByClass(t *testing.T) {
	l := extract.NewLocals()
	if !l.DeclareProperty("src/a.ts/App.constructor", "late", "Client") {
		t.Error("a member of a class declares its properties")
	}
	if l.DeclareProperty("src/a.ts/top", "loose", "Client") {
		t.Error("a function outside a class declares no property")
	}
	l.DeclareProperty("src/a.ts/App.constructor", "plain", "")
	for _, c := range []struct{ member, qualifier, want string }{
		{"src/a.ts/App.run", "this.late", "Client"}, {"src/a.ts/App.run", "this.late.inner", "Client"},
		{"src/a.ts/Other.run", "this.late", ""}, {"src/a.ts/App.run", "this", ""}, {"src/a.ts/top", "this.loose", ""},
		{"src/a.ts/App.run", "late", ""}, {"src/a.ts/App.run", "this.plain", ""},
	} {
		if got := l.TypeOf(c.member, c.qualifier); got != c.want {
			t.Errorf("TypeOf(%q, %q) = %q, want %q", c.member, c.qualifier, got, c.want)
		}
	}
	if !l.PropertyDeclared("src/a.ts/App.other", "late") || l.PropertyDeclared("src/a.ts/top", "loose") {
		t.Error("a property is declared for its class, not for a function outside one")
	}
	if extract.LocalSegments("this.late.x") != 2 || extract.LocalSegments("srv.x") != 1 {
		t.Error("this.x stands for two segments, a variable for one")
	}
}

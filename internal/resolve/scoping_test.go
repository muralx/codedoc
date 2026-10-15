package resolve_test

import (
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/config"
)

// scopingWS indexes one repo of the files given as path, content pairs.
func scopingWS(t *testing.T, name string, files ...string) *ws {
	t.Helper()
	w := newWS(t)
	r := &config.Repo{Name: name, Path: t.TempDir()}
	for i := 0; i < len(files); i += 2 {
		write(t, r.Path, files[i], files[i+1])
	}
	w.repos = append(w.repos, r)
	w.indexAll()
	return w
}

func TestGoDotImportResolvesBareNames(t *testing.T) {
	w := scopingWS(t, "dot", "go.mod", "module example.com/d\n",
		"lib/lib.go", "package lib\n\nfunc Helper() {}\n\nfunc Shadowed() {}\n",
		"main.go", "package main\n\nimport . \"example.com/d/lib\"\n\nfunc main() { Helper(); Shadowed() }\n\nfunc Shadowed() {}\n")
	main := "dot:go:example.com/d:main"
	w.want(w.out(main, "calls"), main+" -> dot:go:example.com/d/lib:Helper [calls call import]",
		main+" -> dot:go:example.com/d:Shadowed [calls call local]")
}

func TestPythonStarImportResolvesBareNames(t *testing.T) {
	w := scopingWS(t, "sp", "pyproject.toml", "[project]\nname = \"sp\"\n", "src/sp/__init__.py", "",
		"src/sp/util.py", "def helper():\n    pass\n",
		"src/sp/main.py", "from sp.util import *\n\ndef run():\n    helper()\n    missing()\n")
	run := "sp:py:sp.main:run"
	got := w.out(run, "calls")
	w.want(got, run+" -> sp:py:sp.util:helper [calls call import]")
	if len(got) != 1 {
		t.Errorf("missing() names nothing: %v", got)
	}
}

func TestJavaBareCallsResolveThroughInheritance(t *testing.T) {
	w := scopingWS(t, "jx",
		"src/main/java/org/x/Base.java", "package org.x;\n\npublic class Base {\n  protected void greet() {}\n}\n",
		"src/main/java/org/y/Mid.java", "package org.y;\n\nimport org.x.Base;\n\npublic class Mid extends Base {}\n",
		"src/main/java/org/y/Child.java", "package org.y;\n\npublic class Child extends Mid {\n  void run() { greet(); }\n}\n",
		"src/main/java/org/y/Other.java", "package org.y;\n\nclass Other {\n  void greet() {}\n}\n")
	run := "jx:java:org.y:Child.run#" + sigOf(t, w, "jx:java:org.y:Child.run")
	greet := "jx:java:org.x:Base.greet#" + sigOf(t, w, "jx:java:org.x:Base.greet")
	got := w.out(run, "calls")
	if len(got) != 1 {
		t.Fatalf("one edge expected: %v", got)
	}
	w.want(got, run+" -> "+greet+" [calls call import]")
}

func TestImplementsExpandsEmbeddedInterfaces(t *testing.T) {
	w := scopingWS(t, "emb", "go.mod", "module example.com/e\n",
		"api/api.go", "package api\n\nimport \"io\"\n\ntype Getter interface{ Get(k string) string }\n\n"+
			"type Store interface {\n\tGetter\n\tPut(k, v string)\n}\n\n"+
			"type Closer interface {\n\tio.Closer\n\tGet(k string) string\n}\n",
		"mem/mem.go", "package mem\n\nimport \"example.com/e/api\"\n\nvar _ api.Store\n\ntype Half struct{}\n\n"+
			"func (Half) Get(k string) string { return k }\n\ntype Full struct{}\n\n"+
			"func (Full) Get(k string) string { return k }\n\nfunc (Full) Put(k, v string) {}\n")
	api := "emb:go:example.com/e/api:"
	full, half := "emb:go:example.com/e/mem:Full", "emb:go:example.com/e/mem:Half"
	w.want(w.out(full, "implements"), full+" -> "+api+"Store [implements implement unique]",
		full+" -> "+api+"Getter [implements implement unique]")
	for _, e := range append(w.out(half, "implements"), w.out(full, "implements")...) {
		if strings.Contains(e, api+"Store") && strings.HasPrefix(e, half) || strings.Contains(e, "Closer") {
			t.Errorf("unexpected %s", e)
		}
	}
	write(t, w.repos[0].Path, "mem/more.go", "package mem\n\nfunc (Half) Put(k, v string) {}\n")
	w.ensure()
	w.want(w.out(half, "implements"), half+" -> "+api+"Store [implements implement unique]")
}

func TestPythonAliasedImportResolvesUnderTheOriginalName(t *testing.T) {
	w := scopingWS(t, "pa", "pyproject.toml", "[project]\nname = \"pa\"\n", "src/pa/__init__.py", "",
		"src/pa/core/__init__.py", "", "src/pa/core/engine.py", "class Engine:\n    pass\n",
		"src/pa/app/__init__.py", "",
		"src/pa/app/helper.py", "from ..core.engine import Engine as Eng\n\n\nclass Helper(Eng):\n    def make(self):\n        return Eng()\n")
	engine := "pa:py:pa.core.engine:Engine"
	w.want(w.out("pa:py:pa.app.helper:Helper", "extends"), "pa:py:pa.app.helper:Helper -> "+engine+" [extends extend import]")
	w.want(w.out("pa:py:pa.app.helper:Helper.make", "references"),
		"pa:py:pa.app.helper:Helper.make -> "+engine+" [references instantiate import]")
}

func TestJavaConstantsThroughAClassQualifier(t *testing.T) {
	w := scopingWS(t, "jc",
		"src/main/java/org/x/Status.java", "package org.x;\n\npublic enum Status {\n  ACTIVE, GONE;\n  public void ACTIVE() {}\n}\n",
		"src/main/java/org/x/Limits.java", "package org.x;\n\npublic class Limits {\n  public static final int MAX = 3;\n}\n",
		"src/main/java/org/y/Use.java", "package org.y;\n\nimport org.x.Status;\nimport org.x.Limits;\n\n"+
			"class Use {\n  Object a() { return Status.ACTIVE; }\n  int b() { return Limits.MAX; }\n}\n",
		"src/main/java/org/x/Local.java", "package org.x;\n\nclass Local {\n  int c() { return Limits.MAX; }\n}\n")
	a := "jc:java:org.y:Use.a#" + sigOf(t, w, "jc:java:org.y:Use.a")
	b := "jc:java:org.y:Use.b#" + sigOf(t, w, "jc:java:org.y:Use.b")
	c := "jc:java:org.x:Local.c#" + sigOf(t, w, "jc:java:org.x:Local.c")
	got := w.out(a, "references")
	w.want(got, a+" -> jc:java:org.x:Status.ACTIVE [references field import]")
	if len(got) != 1 {
		t.Errorf("a field reference names no method: %v", got)
	}
	w.want(w.out(b, "references"), b+" -> jc:java:org.x:Limits.MAX [references field import]")
	w.want(w.out(c, "references"), c+" -> jc:java:org.x:Limits.MAX [references field local]")
}

func TestJavaModuleIdentityPrefersTheDeclaredPackage(t *testing.T) {
	w := scopingWS(t, "bz",
		"java/com/acme/app/App.java", "/* Copyright. */\npackage com.acme.app;\n\nclass App {\n  void run() { new Helper(); }\n}\n",
		"legacy/Helper.java", "// Moved here.\n@Deprecated\npackage com.acme.app;\n\npublic class Helper {}\n",
		"other/Plain.java", "class Plain {}\n")
	for dir, want := range map[string]string{"java/com/acme/app": "com.acme.app", "legacy": "com.acme.app", "other": "other"} {
		m, err := w.s.ModuleByPath(w.repo("bz"), "java", dir)
		if err != nil || m.Identity != want {
			t.Errorf("%s: %+v %v", dir, m, err)
		}
	}
	run := "bz:java:com.acme.app:App.run#" + sigOf(t, w, "bz:java:com.acme.app:App.run")
	w.want(w.out(run, "references"), run+" -> bz:java:com.acme.app:Helper [references instantiate local]")
}

package resolve_test

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/config"
	"github.com/muralx/codedoc/internal/index"
)

const (
	pyStore   = "py-lib:py:pylib.store:"
	pyService = "py-lib:py:pylib.service:"
	jStore    = "java-lib:java:com.fixture.store:"
	jService  = "java-lib:java:com.fixture.service:"
)

func langWS(t *testing.T) *ws {
	w := newWS(t, [2]string{"py-lib", "py-lib"}, [2]string{"java-lib", "java-lib"})
	w.indexAll()
	return w
}

func TestPythonImportsResolve(t *testing.T) {
	w := langWS(t)
	store := w.module("py-lib", "py", "src/pylib/store.py")
	pkg := w.module("py-lib", "py", "src/pylib")
	if got := w.imports("py-lib", "src/pylib/service.py"); got["pylib.store"] != store || got["."] != pkg {
		t.Errorf("service.py imports %v (store %d, package %d)", got, store, pkg)
	}
	if got := w.imports("py-lib", "src/pylib/__init__.py"); got[".store"] != store {
		t.Errorf("__init__.py imports %v", got)
	}
	if got := w.imports("py-lib", "tests/test_store.py"); got["pylib.store"] != store {
		t.Errorf("test imports %v", got)
	}
	if got := w.imports("py-lib", "src/pylib/store.py"); got["abc"] != 0 || got["os"] != 0 {
		t.Errorf("standard library imports resolve to nothing: %v", got)
	}
	if !w.edges(w.module("py-lib", "py", "src/pylib/service.py"))[store] {
		t.Error("no module edge service -> store")
	}
	if !w.edges(pkg)[store] {
		t.Error("no module edge from the package (its __init__.py) to store")
	}
}

func TestPythonTiers(t *testing.T) {
	w := langWS(t)
	w.want(w.out(pyService+"Service.lookup", "calls"),
		pyService+"Service.lookup -> "+pyService+"fallback [calls call local]",
		pyService+"Service.lookup -> "+pyStore+"MemStore.get|"+pyStore+"Store.get [calls call ambiguous]")
	w.want(w.out(pyService+"Service.lookup", "references"),
		pyService+"Service.lookup -> "+pyService+"Service.store [references field local]")
	w.want(w.out(pyService+"fallback", "references"),
		pyService+"fallback -> "+pyStore+"DEFAULT_TIMEOUT [references field import]")
	w.want(w.out(pyService+"Service.__init__", "references"),
		pyService+"Service.__init__ -> "+pyStore+"Store [references type import]")
	w.want(w.out(pyStore+"MemStore", "extends"), pyStore+"MemStore -> "+pyStore+"Store [extends extend local]")
	w.want(w.out("py-lib:py:tests.test_store:test_put_get", "references"),
		"py-lib:py:tests.test_store:test_put_get -> "+pyStore+"MemStore [references instantiate import]")
	w.want(w.out(pyStore+"MemStore.get", "calls"), pyStore+"MemStore.get -> "+pyStore+"_missing [calls call local]")
}

func TestJavaImportsResolve(t *testing.T) {
	w := langWS(t)
	main := w.module("java-lib", "java", "src/main/java/com/fixture/store")
	got := w.imports("java-lib", "src/main/java/com/fixture/service/Service.java")
	if got["com.fixture.store.Store"] != main || got["java.util.*"] != 0 {
		t.Errorf("Service.java imports %v (store %d)", got, main)
	}
	if !w.edges(w.module("java-lib", "java", "src/main/java/com/fixture/service"))[main] {
		t.Error("no module edge service -> store")
	}
}

func TestJavaTiers(t *testing.T) {
	w := langWS(t)
	k := func(prefix string) string { return prefix + "#" + sigOf(t, w, prefix) }
	lookup := k(jService + "Service.lookup")
	w.want(w.out(lookup, "calls"),
		lookup+" -> "+k(jStore+"Store.get")+" [calls call import]",
		lookup+" -> "+k(jService+"Service.fallback")+" [calls call local]")
	w.want(w.out(jStore+"MemStore", "implements"), jStore+"MemStore -> "+jStore+"Store [implements implement local]")
	test := k(jStore + "MemStoreTest.putThenGet")
	w.want(w.out(test, "references"), test+" -> "+jStore+"MemStore [references instantiate local]")
	puts := keysWithPrefix(t, w, jStore+"MemStore.put#")
	if len(puts) != 2 {
		t.Fatalf("put overloads %v", puts)
	}
	for _, put := range puts {
		found := false
		for _, e := range w.in(put, "calls") {
			if regexp.MustCompile(`^` + regexp.QuoteMeta(test) + ` -> .* \[calls call ambiguous\]$`).MatchString(e) {
				found = true
			}
		}
		if !found {
			t.Errorf("putThenGet is not among the callers of %s: %v", put, w.in(put, "calls"))
		}
	}
	ctor := k(jService + "Service.<init>")
	w.want(w.out(ctor, "references"), ctor+" -> "+jStore+"Store [references type import]")
}

// sigOf returns the signature hash of the one symbol whose key starts
// with prefix#.
func sigOf(t *testing.T, w *ws, prefix string) string {
	t.Helper()
	keys := keysWithPrefix(t, w, prefix+"#")
	if len(keys) != 1 {
		t.Fatalf("%s: %v", prefix, keys)
	}
	return keys[0][len(prefix)+1:]
}

// keysWithPrefix returns the sorted stable keys starting with prefix
// among the symbols named like its last qualified name segment.
func keysWithPrefix(t *testing.T, w *ws, prefix string) []string {
	t.Helper()
	q := strings.TrimSuffix(prefix, "#")
	name := q[strings.LastIndexAny(q, ".:")+1:]
	syms, err := w.s.SymbolsByName(name, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, sy := range syms {
		if strings.HasPrefix(sy.StableKey, prefix) {
			out = append(out, sy.StableKey)
		}
	}
	sort.Strings(out)
	return out
}

// appRepos adds a Python and a Java repo importing the py-lib and
// java-lib fixtures to the workspace.
func appRepos(t *testing.T, w *ws) (py, java *config.Repo) {
	t.Helper()
	py = &config.Repo{Name: "pyapp", Path: t.TempDir()}
	write(t, py.Path, "pyproject.toml", "[project]\nname = \"pyapp\"\n")
	write(t, py.Path, "src/app/__init__.py", "")
	write(t, py.Path, "src/app/main.py", "import pylib.store as st\nfrom ... import nothing\nfrom . import util\n\n"+
		"def run():\n    st.MemStore()\n    util.helper()\n")
	write(t, py.Path, "src/app/util.py", "def helper():\n    pass\n")
	java = &config.Repo{Name: "japp", Path: t.TempDir()}
	write(t, java.Path, "src/main/java/org/app/App.java",
		"package org.app;\n\nimport com.fixture.store.*;\n\nclass App {\n  void run() { new MemStore(); }\n}\n")
	w.repos = append(w.repos, py, java)
	return py, java
}

func TestCrossRepoPythonAndJavaWildcard(t *testing.T) {
	w := newWS(t, [2]string{"py-lib", "py-lib"}, [2]string{"java-lib", "java-lib"})
	py, _ := appRepos(t, w)
	w.indexAll()
	store := w.module("py-lib", "py", "src/pylib/store.py")
	if got := w.imports("pyapp", "src/app/main.py"); got["pylib.store"] != store || got["..."] != 0 {
		t.Errorf("imports %v", got)
	}
	run := "pyapp:py:app.main:run"
	w.want(w.out(run, "references"), run+" -> "+pyStore+"MemStore [references instantiate import]")
	w.want(w.out(run, "calls"), run+" -> pyapp:py:app.util:helper [calls call import]")
	jrun := "japp:java:org.app:App.run#" + sigOf(t, w, "japp:java:org.app:App.run")
	w.want(w.out(jrun, "references"), jrun+" -> "+jStore+"MemStore [references instantiate import]")
	write(t, py.Path, "src/app/main.py", "from pylib import store\n\ndef run():\n    store.MemStore()\n")
	if _, err := index.Ensure(w.s, py, w.opts()); err != nil {
		t.Fatal(err)
	}
	w.want(w.out(run, "references"), run+" -> "+pyStore+"MemStore [references instantiate import]")
	if !w.edges(w.module("pyapp", "py", "src/app/main.py"))[w.module("py-lib", "py", "src/pylib")] {
		t.Error("no module edge main -> pylib after the edit")
	}
}

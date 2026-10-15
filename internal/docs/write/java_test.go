package write

import (
	"strings"
	"testing"
)

const javaSrc = `package com.x;

public class Svc {
    /**
     * Old text.
     *
     * @param id the id
     *     spanning lines
     * @return the name
     * @see Other
     */
    @Override
    public String name(int id) { return ""; }

    @Deprecated
    public void run(int a) {}

    public void run(String s) {}
}
`

// sigHashOf returns the signature hash of the nth symbol named q.
func sigHashOf(t *testing.T, src, q string, nth int) string {
	t.Helper()
	res, err := extractSource("java", "Svc.java", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range res.Symbols {
		if s.Qualified == q {
			if nth == 0 {
				return s.SigHash
			}
			nth--
		}
	}
	t.Fatalf("%s not found", q)
	return ""
}

func TestJavaSymbolDocs(t *testing.T) {
	files := map[string]string{"Svc.java": javaSrc}
	tg := func(q, sig string) Target { return Target{Path: "Svc.java", Lang: "java", Qualified: q, SigHash: sig} }
	kept := "    /**\n     * Returns the name.\n     *\n     * @param id the id\n     *     spanning lines\n     * @return the name\n     * @see Other\n     */\n"
	oldDoc := javaSrc[strings.Index(javaSrc, "    /**"):strings.Index(javaSrc, "    @Override")]
	first := sigHashOf(t, javaSrc, "Svc.run", 0)
	runRoundTrips(t, []roundTrip{
		{name: "replace keeps tags", files: files, tg: tg("Svc.name", ""), text: "Returns the name.", file: "Svc.java",
			want: strings.Replace(javaSrc, oldDoc, kept, 1),
			doc:  "Returns the name.\n\n@param id the id\n    spanning lines\n@return the name\n@see Other"},
		{name: "new tags replace old", files: files, tg: tg("Svc.name", ""), text: "Name.\n@param id key", file: "Svc.java",
			want: strings.Replace(javaSrc, oldDoc, "    /**\n     * Name.\n     * @param id key\n     */\n", 1),
			doc:  "Name.\n@param id key"},
		{name: "overload above annotation", files: files, tg: tg("Svc.run", first), text: "Runs a.", file: "Svc.java",
			want: strings.Replace(javaSrc, "    @Deprecated\n", "    /** Runs a. */\n    @Deprecated\n", 1), doc: "Runs a."},
	})
	root := repo(t, files)
	if _, err := Plan(Target{Root: root, Path: "Svc.java", Lang: "java", Qualified: "Svc.run"}, "x"); err == nil ||
		!strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("overloads without a hash: %v", err)
	}
	if _, err := Plan(Target{Root: root, Path: "Svc.java", Lang: "java", Qualified: "Svc.nope"}, "x"); err == nil {
		t.Fatal("missing symbol accepted")
	}
}

func TestJavaModuleDocs(t *testing.T) {
	tg := Target{Path: "src/com/x", Lang: "java", ModuleDir: "src/com/x"}
	runRoundTrips(t, []roundTrip{
		{name: "create package-info", files: map[string]string{"src/com/x/A.java": "package com.x;\nclass A {}\n"}, tg: tg,
			text: "Services.", file: "src/com/x/package-info.java", want: "/** Services. */\npackage com.x;\n", doc: "Services."},
		{name: "replace", files: map[string]string{"src/com/x/package-info.java": "/**\n * Old.\n */\n@Ann\npackage com.x;\n"}, tg: tg,
			text: "New.\n\nMore.", file: "src/com/x/package-info.java",
			want: "/**\n * New.\n *\n * More.\n */\n@Ann\npackage com.x;\n", doc: "New.\n\nMore."},
		{name: "insert", files: map[string]string{"src/com/x/package-info.java": "// license\n\npackage com.x;\n"}, tg: tg,
			text: "New.", file: "src/com/x/package-info.java", want: "// license\n\n/** New. */\npackage com.x;\n", doc: "New."},
	})
	root := repo(t, map[string]string{"a/package-info.java": "// nothing\n", "b/x.txt": ""})
	for _, dir := range []string{"a", "b"} {
		if _, err := Plan(Target{Root: root, Path: dir, Lang: "java", ModuleDir: dir}, "x"); err == nil {
			t.Fatalf("%s: accepted", dir)
		}
	}
	if _, err := Plan(Target{Root: root, Path: "a", Lang: "java", ModuleDir: "a"}, "a */"); err == nil {
		t.Fatal("*/ accepted")
	}
}

package java_test

import (
	"testing"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/extract/golden"
	"github.com/muralx/codedoc/internal/extract/java"
)

func TestGolden(t *testing.T) {
	golden.RunFixtures(t, java.New(), golden.Sources(t, "java-", ".java"))
}

func TestRegistered(t *testing.T) {
	if _, ok := extract.For("java"); !ok {
		t.Fatal("java extractor not registered")
	}
}

func extractSrc(t *testing.T, path, src string) *extract.FileResult {
	t.Helper()
	res, err := java.New().Extract(path, []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

const edgeCases = `/** License header, not a doc. */
package a.b;

/** Outer doc. */
@Deprecated
public abstract class Outer<T> {
    /** Two fields. */
    protected int x, y = 2;
    public static final String NAME = "n";
    static String notConst;

    /** Generic overload. */
    @SafeVarargs
    public final <K> void put(java.util.Map<K, List<T>> m, int[] xs, String... rest) {}
    void put(@Nonnull String s) {}
    abstract void put(final Map.Entry<String, Integer> e);
    void put(String other) {}

    Outer(int x) {}
    private Outer() { this(1); }

    interface Inner {
        void m();
        private void hidden() {}
        int LIMIT = 3;
        class Deep {}
    }

    enum Color {
        /** The red one. */
        RED,
        GREEN;
        Color() {}
        String label() { return name(); }
    }

    record Point(int x, int y) {}

    @interface Marker {
        String value() default "";
    }
}

class Package {}
`

type row struct {
	kind extract.Kind
	q    string
	vis  extract.Visibility
}

var edgeSymbols = []row{
	{extract.KindClass, "Outer", extract.Public},
	{extract.KindField, "Outer.x", extract.Package},
	{extract.KindField, "Outer.y", extract.Package},
	{extract.KindConst, "Outer.NAME", extract.Public},
	{extract.KindField, "Outer.notConst", extract.Package},
	{extract.KindMethod, "Outer.put", extract.Public},
	{extract.KindMethod, "Outer.put", extract.Package},
	{extract.KindMethod, "Outer.put", extract.Package},
	{extract.KindMethod, "Outer.put~2", extract.Package},
	{extract.KindMethod, "Outer.<init>", extract.Package},
	{extract.KindMethod, "Outer.<init>", extract.Private},
	{extract.KindInterface, "Outer.Inner", extract.Package},
	{extract.KindMethod, "Outer.Inner.m", extract.Public},
	{extract.KindMethod, "Outer.Inner.hidden", extract.Private},
	{extract.KindConst, "Outer.Inner.LIMIT", extract.Public},
	{extract.KindClass, "Outer.Inner.Deep", extract.Public},
	{extract.KindEnum, "Outer.Color", extract.Package},
	{extract.KindConst, "Outer.Color.RED", extract.Public},
	{extract.KindConst, "Outer.Color.GREEN", extract.Public},
	{extract.KindMethod, "Outer.Color.<init>", extract.Package},
	{extract.KindMethod, "Outer.Color.label", extract.Package},
	{extract.KindClass, "Outer.Point", extract.Package},
	{extract.KindField, "Outer.Point.x", extract.Private},
	{extract.KindMethod, "Outer.Point.x", extract.Public},
	{extract.KindField, "Outer.Point.y", extract.Private},
	{extract.KindMethod, "Outer.Point.y", extract.Public},
	{extract.KindInterface, "Outer.Marker", extract.Package},
	{extract.KindMethod, "Outer.Marker.value", extract.Public},
	{extract.KindClass, "Package", extract.Package},
}

func TestEdgeCases(t *testing.T) {
	res := extractSrc(t, "src/main/java/a/b/Outer.java", edgeCases)
	if len(res.Symbols) != len(edgeSymbols) {
		for _, s := range res.Symbols {
			t.Logf("%s %s %s", s.Kind, s.Qualified, s.Visibility)
		}
		t.Fatalf("got %d symbols, want %d", len(res.Symbols), len(edgeSymbols))
	}
	for i, w := range edgeSymbols {
		if s := res.Symbols[i]; s.Kind != w.kind || s.Qualified != w.q || s.Visibility != w.vis {
			t.Errorf("symbol %d: got %s %s %s, want %+v", i, s.Kind, s.Qualified, s.Visibility, w)
		}
	}
	if res.ModuleName != "a.b" || res.ModuleDoc != nil {
		t.Errorf("module %q doc %+v", res.ModuleName, res.ModuleDoc)
	}
	checkDocsAndSignatures(t, res.Symbols)
	checkSigHashes(t, res.Symbols)
}

func checkDocsAndSignatures(t *testing.T, syms []extract.Symbol) {
	t.Helper()
	docs := map[int]string{0: "Outer doc.", 1: "Two fields.", 2: "Two fields.", 5: "Generic overload.", 17: "The red one."}
	for i, want := range docs {
		if d := syms[i].Doc; d == nil || d.Text != want {
			t.Errorf("%s: doc %+v", syms[i].Qualified, d)
		}
	}
	sigs := map[int]string{
		0: "public abstract class Outer<T>",
		1: "protected int x, y = 2",
		2: "protected int x, y = 2",
		5: "public final <K> void put(java.util.Map<K, List<T>> m, int[] xs, String... rest)",
		7: "abstract void put(final Map.Entry<String, Integer> e)",
	}
	for i, want := range sigs {
		if syms[i].SigText != want {
			t.Errorf("%s: signature %q", syms[i].Qualified, syms[i].SigText)
		}
	}
	if s := syms[0]; s.Span.StartLine != 5 || s.Signature.StartLine != 6 {
		t.Errorf("Outer spans %+v %+v", s.Span, s.Signature)
	}
	if s := syms[5]; s.Span.StartLine != 13 || s.Signature.StartLine != 14 {
		t.Errorf("put spans %+v %+v", s.Span, s.Signature)
	}
}

func checkSigHashes(t *testing.T, syms []extract.Symbol) {
	t.Helper()
	keys := map[string]bool{}
	for _, i := range []int{5, 6, 7, 8, 9, 10, 12, 19, 23, 25, 27} {
		if len(syms[i].SigHash) != 4 {
			t.Errorf("%s: SigHash %q", syms[i].Qualified, syms[i].SigHash)
		}
		keys[syms[i].Qualified+"#"+syms[i].SigHash] = true
	}
	if syms[6].SigHash != syms[8].SigHash || len(keys) != 11 {
		t.Errorf("String and @Nonnull String must hash alike, other keys distinct: %v", keys)
	}
	if syms[0].SigHash != "" || syms[1].SigHash != "" {
		t.Error("only methods and constructors carry a SigHash")
	}
}

func TestSigHashNormalization(t *testing.T) {
	hash := func(params string) string {
		t.Helper()
		res := extractSrc(t, "A.java", "class A { void m("+params+") {} }")
		return res.Symbols[1].SigHash
	}
	same := [][2]string{
		{"List<String> a", "java.util.List<Integer> b"},
		{"String... a", "final String ... b"},
		{"int[] a", "int a[]"},
		{"Map.Entry<K, V> e", "Entry e"},
	}
	for _, p := range same {
		if hash(p[0]) != hash(p[1]) {
			t.Errorf("%q and %q differ", p[0], p[1])
		}
	}
	if hash("String[] a") == hash("String... a") || hash("int a") == hash("long a") || hash("") == hash("int a") {
		t.Error("distinct parameter lists hash alike")
	}
}

func TestTestsAndModuleDoc(t *testing.T) {
	src := "package p;\nimport org.junit.jupiter.api.Test;\nclass T {\n  @Test void a() {}\n" +
		"  @org.junit.jupiter.params.ParameterizedTest void b(int x) {}\n  @RepeatedTest(3) void c() {}\n  void d() {}\n}\n"
	wantTests := map[string]bool{"T.a": true, "T.b": true, "T.c": true}
	for _, s := range extractSrc(t, "mod/src/test/java/p/T.java", src).Symbols {
		if s.IsTest != wantTests[s.Qualified] {
			t.Errorf("%s: IsTest %v", s.Qualified, s.IsTest)
		}
	}
	for _, s := range extractSrc(t, "src/main/java/p/T.java", src).Symbols {
		if s.IsTest {
			t.Errorf("%s: a test outside src/test", s.Qualified)
		}
	}
	res := extractSrc(t, "src/main/java/p/package-info.java", "/**\n * Package p.\n *\n * More.\n */\npackage p;\n")
	if res.ModuleDoc == nil || res.ModuleDoc.Text != "Package p.\n\nMore." || res.ModuleName != "p" {
		t.Errorf("package doc %+v %q", res.ModuleDoc, res.ModuleName)
	}
}

func TestParseErrors(t *testing.T) {
	if res := extractSrc(t, "A.java", "class A { void m( }"); res.ParseErrors == 0 {
		t.Error("parse errors not counted")
	}
}

func TestSigHashAnnotatedType(t *testing.T) {
	a := extractSrc(t, "A.java", "class A { void m(java.util.@Nonnull List<String> a) {} }").Symbols[1]
	b := extractSrc(t, "A.java", "class A { void m(List<Integer> a) {} }").Symbols[1]
	if a.SigHash != b.SigHash {
		t.Errorf("type annotations change the hash: %s %s", a.SigHash, b.SigHash)
	}
}

func TestBodyHashCoversSignature(t *testing.T) {
	src := func(param string) string {
		return "package p;\nclass A {\n  /** Doc. */\n  int f(int " + param + ") { return 1; }\n}\n"
	}
	a, b := extractSrc(t, "A.java", src("x")), extractSrc(t, "A.java", src("y"))
	if a.Symbols[1].BodyHash == b.Symbols[1].BodyHash {
		t.Error("renaming a parameter kept the body hash")
	}
}

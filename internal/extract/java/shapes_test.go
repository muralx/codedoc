package java_test

import (
	"github.com/muralx/codedoc/internal/testutil"
	"reflect"
	"testing"

	"github.com/muralx/codedoc/internal/extract"
)

const annotationSrc = `package p;

@JsonDeserialize(using = Foo.class)
@a.b.Ann(Bar.class)
class C {
    @Override @Deprecated @SuppressWarnings("x") @FunctionalInterface @SafeVarargs
    @Inject
    void m() {}
}
`

func TestAnnotationReferences(t *testing.T) {
	res := extractSrc(t, "src/main/java/p/C.java", annotationSrc)
	want := []extract.Reference{
		ref(extract.RefType, "JsonDeserialize", "", "C", 3, -1),
		ref(extract.RefType, "Foo", "", "C", 3, -1),
		ref(extract.RefType, "Ann", "a.b", "C", 4, -1),
		ref(extract.RefType, "Bar", "", "C", 4, -1),
		ref(extract.RefType, "Inject", "", "C.m", 7, -1),
	}
	if !reflect.DeepEqual(testutil.WithoutCols(res.References), want) {
		for _, r := range res.References {
			t.Logf("%+v", r)
		}
		t.Errorf("references differ")
	}
}

const qualifiedSrc = `class C {
    void m() {
        java.util.List.of();
        java.util.Locale.US.toString();
        new Controller().run();
        this.items.size();
        java.util.Map.Entry.comparingByKey();
    }
}
`

func TestFullyQualifiedExpressions(t *testing.T) {
	res := extractSrc(t, "C.java", qualifiedSrc)
	const m = "C.m"
	want := []extract.Reference{
		ref(extract.RefCall, "of", "List", m, 3, 0),
		ref(extract.RefType, "List", "java.util", m, 3, -1),
		ref(extract.RefCall, "toString", "Locale.US", m, 4, 0),
		ref(extract.RefType, "Locale", "java.util", m, 4, -1),
		ref(extract.RefField, "US", "Locale", m, 4, -1),
		ref(extract.RefCall, "run", "Controller()", m, 5, 0),
		ref(extract.RefInstantiate, "Controller", "", m, 5, 0),
		ref(extract.RefCall, "size", "this.items", m, 6, 0),
		ref(extract.RefField, "items", "this", m, 6, -1),
		ref(extract.RefCall, "comparingByKey", "Map.Entry", m, 7, 0),
		ref(extract.RefType, "Map", "java.util", m, 7, -1),
		ref(extract.RefField, "Entry", "Map", m, 7, -1),
	}
	if !reflect.DeepEqual(testutil.WithoutCols(res.References), want) {
		for _, r := range res.References {
			t.Logf("%+v", r)
		}
		t.Errorf("references differ")
	}
}

const recordSrc = `record Point(int x, @Nullable java.util.List<String> tags) {
    Point {
        java.util.Objects.requireNonNull(tags);
    }
    public int x() { return x; }
    static Point origin(Point p) { return new Point(p.x(), p.tags()); }
}
`

func TestRecords(t *testing.T) {
	res := extractSrc(t, "Point.java", recordSrc)
	var got []string
	for _, s := range res.Symbols {
		got = append(got, string(s.Kind)+" "+s.Qualified+" "+s.Container+" "+string(s.Visibility)+" "+s.SigText)
	}
	want := []string{
		"class Point  package record Point(int x, @Nullable java.util.List<String> tags)",
		"field Point.x Point private int x",
		"field Point.tags Point private java.util.List<String> tags",
		"method Point.tags Point public tags()",
		"method Point.<init> Point package Point",
		"method Point.x Point public public int x()",
		"method Point.origin Point package static Point origin(Point p)",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("symbols:\n got %q\nwant %q", got, want)
	}
	byQ := map[string]extract.Symbol{}
	for _, s := range res.Symbols {
		byQ[s.Qualified+"/"+string(s.Kind)] = s
	}
	canonical := extractSrc(t, "P.java", "class P { P(int x, java.util.List<String> tags) {} }").Symbols[1].SigHash
	if h := byQ["Point.<init>/method"].SigHash; h != canonical {
		t.Errorf("compact constructor hash %q, canonical %q", h, canonical)
	}
	if h, empty := byQ["Point.tags/method"].SigHash, byQ["Point.x/method"].SigHash; h == "" || h != empty {
		t.Errorf("accessor hash %q, want the empty-list hash %q", h, empty)
	}
	if e := enclosingOf(res, "requireNonNull"); e != "Point.<init>" {
		t.Errorf("compact constructor encloses %q", e)
	}
}

func enclosingOf(res *extract.FileResult, name string) string {
	for _, r := range res.References {
		if r.Name == name {
			return r.Enclosing
		}
	}
	return ""
}

func TestTestFileDetection(t *testing.T) {
	src := "class T {\n  @Test void a() {}\n  void b() {}\n}\n"
	for path, want := range map[string]bool{
		"src/test/java/p/T.java":        true,
		"mod/javatests/p/T.java":        true,
		"javatests/p/T.java":            true,
		"src/main/java/p/FooTest.java":  true,
		"src/main/java/p/FooTests.java": true,
		"src/main/java/p/FooIT.java":    true,
		"src/main/java/p/T.java":        false,
		"src/main/java/p/Contest.java":  false,
	} {
		syms := extractSrc(t, path, src).Symbols
		if syms[1].IsTest != want || syms[2].IsTest {
			t.Errorf("%s: a IsTest %v, b IsTest %v", path, syms[1].IsTest, syms[2].IsTest)
		}
	}
}

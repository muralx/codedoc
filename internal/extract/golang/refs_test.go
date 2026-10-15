package golang_test

import (
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/testutil"
)

func hasRef(refs []extract.Reference, want extract.Reference) bool {
	return slices.Contains(testutil.WithoutCols(refs), want)
}

func TestServiceReferences(t *testing.T) {
	src, err := os.ReadFile(testutil.WorkspaceDir() + "/go-lib/service/service.go")
	if err != nil {
		t.Fatal(err)
	}
	res := extractSrc(t, "service/service.go", string(src))
	for _, want := range []extract.Reference{
		{Kind: extract.RefCall, Name: "Get", Qualifier: "svc.s", Enclosing: "Service.Lookup", Line: 18, Arity: 1},
		{Kind: extract.RefField, Name: "s", Qualifier: "svc", Enclosing: "Service.Lookup", Line: 18, Arity: -1},
		{Kind: extract.RefCall, Name: "fallback", Enclosing: "Service.Lookup", Line: 20, Arity: 1},
		{Kind: extract.RefType, Name: "Store", Qualifier: "store", Enclosing: "Service.s", Line: 11, Arity: -1},
		{Kind: extract.RefType, Name: "Store", Qualifier: "store", Enclosing: "New", Line: 14, Arity: -1},
		{Kind: extract.RefInstantiate, Name: "Service", Enclosing: "New", Line: 14, Arity: -1},
		{Kind: extract.RefType, Name: "Service", Enclosing: "Service.Lookup", Line: 17, Arity: -1},
		{Kind: extract.RefCall, Name: "Getenv", Qualifier: "os", Enclosing: "Mode", Line: 28, Arity: 1},
		{Kind: extract.RefConfig, Name: "LIB_MODE", Qualifier: extract.ConfigAccessor, Enclosing: "Mode", Line: 28, Arity: -1},
	} {
		if !hasRef(res.References, want) {
			t.Errorf("missing %+v in %+v", want, res.References)
		}
	}
	want := []extract.Import{{Raw: "os", Line: 5}, {Raw: "github.com/fixture/lib/store", Line: 7}}
	if !slices.EqualFunc(res.Imports, want, func(a, b extract.Import) bool { return a.Raw == b.Raw && a.Line == b.Line && a.Alias == b.Alias }) {
		t.Errorf("imports %+v", res.Imports)
	}
}

func TestReferenceEdgeCases(t *testing.T) {
	src := `package p

import (
	f "fmt"
	_ "embed"
)

type T struct{ x List[int] }

func g() {
	_ = List[int]{}
	_ = len("x")
	_ = string([]byte("y"))
	f.Println(T{}.x)
	func() {}()
}
`
	res := extractSrc(t, "p.go", src)
	for _, want := range []extract.Reference{
		{Kind: extract.RefType, Name: "List", Enclosing: "T.x", Line: 8, Arity: -1},
		{Kind: extract.RefInstantiate, Name: "List", Enclosing: "g", Line: 11, Arity: -1},
		{Kind: extract.RefCall, Name: "Println", Qualifier: "f", Enclosing: "g", Line: 14, Arity: 1},
		{Kind: extract.RefField, Name: "x", Qualifier: "()", Enclosing: "g", Line: 14, Arity: -1},
		{Kind: extract.RefInstantiate, Name: "T", Enclosing: "g", Line: 14, Arity: -1},
	} {
		if !hasRef(res.References, want) {
			t.Errorf("missing %+v in %+v", want, res.References)
		}
	}
	// Predeclared names (len, string, byte, int) and the declared name T
	// on line 8 are not references, so the five above are all there is.
	if len(res.References) != 5 {
		t.Errorf("got %d references: %+v", len(res.References), res.References)
	}
	if len(res.Imports) != 2 || res.Imports[0].Alias != "f" || res.Imports[1].Alias != "_" || res.Imports[1].Raw != "embed" {
		t.Errorf("imports %+v", res.Imports)
	}
}

func TestQualifierIsTrailingChain(t *testing.T) {
	long := strings.Repeat("abcdefghij.", 20) + "end"
	src := "package p\n\nfunc g() {\n" +
		"\tx.Do(1).Then()\n" +
		"\tfunc() int { return 1 }().M()\n" +
		"\ta.b.c.D()\n" +
		"\txs[0].F()\n" +
		"\t" + long + ".Z()\n" +
		"\tx.Do().Get(2).H()\n" +
		"\t" + strings.Repeat("w", 130) + ".W()\n" +
		"}\n"
	res := extractSrc(t, "p.go", src)
	quals := map[string]string{}
	for _, r := range res.References {
		if r.Kind == extract.RefCall {
			quals[r.Name] = r.Qualifier
		}
	}
	for name, want := range map[string]string{
		"Then": "x.Do()", "M": "()", "D": "a.b.c", "F": "()", "H": "x.Do().Get()",
	} {
		if quals[name] != want {
			t.Errorf("qualifier of %s = %q, want %q", name, quals[name], want)
		}
	}
	if w := quals["W"]; len(w) != 120 {
		t.Errorf("a single long name is cut to 120 bytes, got %d", len(w))
	}
	z := quals["Z"]
	if len(z) > 120 || !strings.HasSuffix(z, ".end") || strings.HasPrefix(z, ".") || !strings.HasPrefix(z, "abcdefghij.") {
		t.Errorf("long qualifier not capped at a segment boundary: %q (%d bytes)", z, len(z))
	}
}

func TestElidedElementLiteralsInstantiate(t *testing.T) {
	src := "package p\n\nvar (\n" +
		"\ta = []Foo{{}}\n" +
		"\tb = map[K]*Bar{\"x\": {}}\n" +
		"\tc = []pkg.Baz{{}}\n" +
		"\td = []Qux{q}\n" +
		"\te = map[Key]int{{}: 1}\n" +
		")\n"
	res := extractSrc(t, "p.go", src)
	kinds := map[string]extract.RefKind{}
	for _, r := range res.References {
		kinds[r.Name] = r.Kind
	}
	for name, want := range map[string]extract.RefKind{
		"Foo": extract.RefInstantiate, "Bar": extract.RefInstantiate, "Baz": extract.RefInstantiate,
		"K": extract.RefType, "Qux": extract.RefType, "Key": extract.RefInstantiate,
	} {
		if kinds[name] != want {
			t.Errorf("%s: kind %q, want %q", name, kinds[name], want)
		}
	}
}

func TestConfigCandidates(t *testing.T) {
	src := `package p

func f() {
	_ = os.Getenv("LIB_MODE")
	_, _ = os.LookupEnv("x")
	_ = viper.GetString("server.port")
	_ = viper.Get("lower")
	log.Printf("%s", "API_URL", ` + "`raw.key`" + `)
	_ = cfg.Get("not a key")
	func(string) {}("FN_KEY")
}
`
	res := extractSrc(t, "p.go", src)
	var got []string
	for _, r := range res.References {
		if r.Kind == extract.RefConfig {
			if r.Enclosing != "f" || r.Arity != -1 {
				t.Errorf("config ref %+v", r)
			}
			got = append(got, r.Name+"@"+strconv.Itoa(r.Line)+":"+r.Qualifier)
		}
	}
	want := []string{"LIB_MODE@4:accessor", "x@5:accessor", "server.port@6:accessor", "lower@7:accessor",
		"API_URL@8:literal", "raw.key@8:literal", "FN_KEY@10:literal"}
	if !slices.Equal(got, want) {
		t.Errorf("config refs %v, want %v", got, want)
	}
}

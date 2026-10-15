package scip

import (
	"testing"
)

const (
	baseSrc    = "package service\n\n// Base says hello.\ntype Base struct{}\n\n// Hello greets.\nfunc (Base) Hello() string { return \"hi\" }\n"
	wrapperSrc = "package service\n\n// Wrapper wraps a Base.\ntype Wrapper struct{ Base }\n"
	useSrc     = "package service\n\nfunc use(w Wrapper) string {\n\treturn w.Hello()\n}\n"
	keyUse     = "go-lib:go:github.com/fixture/lib/service:use"
)

// helloIndex indexes base.go, wrapper.go and use.go, with the call in
// use.go naming hello; with extra, an occurrence of Base on the return
// keyword, where the extractor records no reference.
func helloIndex(t *testing.T, hello string, extra bool) *Index {
	t.Helper()
	base, baseHello, wrapper, use := scipSym("Base#"), scipSym("Base#Hello()."), scipSym("Wrapper#"), scipSym("use().")
	at := func(path, src string, line int, ident string, sym string, def bool) Occurrence {
		o := occAt(t, src, line, ident, 0, sym, def)
		o.Path = path
		return o
	}
	occ := []Occurrence{
		at("service/base.go", baseSrc, 4, "Base", base, true),
		at("service/base.go", baseSrc, 7, "Hello", baseHello, true),
		at("service/wrapper.go", wrapperSrc, 4, "Wrapper", wrapper, true),
		at("service/use.go", useSrc, 3, "use", use, true),
		at("service/use.go", useSrc, 4, "Hello", scipSym(hello), false),
	}
	if extra {
		occ = append(occ, at("service/use.go", useSrc, 4, "return", base, false))
	}
	return &Index{Occurrences: occ, Symbols: map[string]SymbolInfo{}}
}

func TestMergeDropsWhatItNoLongerConfirms(t *testing.T) {
	f := newLibFixture(t)
	f.write(t, "service/base.go", baseSrc)
	f.write(t, "service/wrapper.go", wrapperSrc)
	f.write(t, "service/use.go", useSrc)
	gitAddCommit(t, f.cfg.Path)
	f.ensure(t)
	keyBaseHello := "go-lib:go:github.com/fixture/lib/service:Base.Hello"
	if st := f.merge(t, helloIndex(t, "Base#Hello().", true)); st.Added != 1 || st.Dropped != 0 {
		t.Fatalf("first merge: %+v", st)
	}
	wantEdge(t, f, keyUse, keyBaseHello, "calls", "precise")
	// Wrapper gets its own Hello, not committed yet: wrapper.go is not
	// trusted, so the call's new target is not mapped and the merge
	// cannot confirm the precise edge to Base.Hello.
	f.appendTo(t, "service/wrapper.go", "\n// Hello greets as a Wrapper.\nfunc (Wrapper) Hello() string { return \"w\" }\n")
	f.ensure(t)
	wantEdge(t, f, keyUse, keyBaseHello, "calls", "precise")
	st := f.merge(t, helloIndex(t, "Wrapper#Hello().", false))
	if st.Dropped != 2 || st.Skipped != 1 {
		t.Fatalf("second merge: %+v", st)
	}
	// The call is resolved again by the heuristic tiers, which see both
	// methods.
	if conf, _ := f.edge(t, keyUse, keyBaseHello, "calls"); conf == "precise" {
		t.Fatal("the precise call to Base.Hello survived")
	}
	if conf, n := f.edge(t, keyUse, "go-lib:go:github.com/fixture/lib/service:Wrapper.Hello", "calls"); n != 1 ||
		conf == "precise" {
		t.Fatalf("call to Wrapper.Hello: %q x%d", conf, n)
	}
	if conf, n := f.edge(t, keyUse, "go-lib:go:github.com/fixture/lib/service:Base", "references"); n != 0 {
		t.Fatalf("the added reference survived (%s)", conf)
	}
}

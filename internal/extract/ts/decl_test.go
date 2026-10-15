package ts_test

import (
	"reflect"
	"testing"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/extract/ts"
)

// symbolsOf extracts src and returns the symbols in source order.
func symbolsOf(t *testing.T, path, src string) []extract.Symbol {
	t.Helper()
	res, err := ts.New("typescript").Extract(path, []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return res.Symbols
}

func docText(s extract.Symbol) string {
	if s.Doc == nil {
		return ""
	}
	return s.Doc.Text
}

func TestDecoratedMethod(t *testing.T) {
	src := "class C {\n  /** decorated method */\n  @Get()\n  handle(): void {}\n}\n"
	m := extractSrc(t, "typescript", "c.ts", src)["c.ts/C.handle"]
	if docText(m) != "decorated method" || m.SigText != "@Get() handle(): void" || m.Span.StartLine != 3 ||
		m.Signature.StartLine != 3 {
		t.Errorf("got doc %q sig %q span %+v", docText(m), m.SigText, m.Span)
	}
}

func TestAccessorsCollapse(t *testing.T) {
	src := "class Comp {\n  /** The value. */\n  get v() { return 1; }\n  set v(x) {}\n  other() {}\n}\n"
	syms := symbolsOf(t, "c.ts", src)
	if len(syms) != 3 {
		t.Fatalf("got %+v", syms)
	}
	v := syms[1]
	if v.Qualified != "c.ts/Comp.v" || v.Kind != extract.KindMethod || docText(v) != "The value." ||
		v.Span.StartLine != 3 || v.Span.EndLine != 4 {
		t.Errorf("accessor: %+v", v)
	}
}

func TestOverloadsTakeFirstDoc(t *testing.T) {
	src := "/** Formats. */\nexport function f(a: string): string;\nexport function f(a: number): string;\n" +
		"export function f(a: unknown): string { return ''; }\n\n" +
		"class K {\n  /** Runs. */\n  run(a: string): void;\n  run(a: unknown): void {}\n}\n" +
		"declare function g(a: string): void;\ndeclare function g(a: number): void;\n"
	syms := extractSrc(t, "typescript", "o.ts", src)
	for _, c := range []struct {
		qualified, doc, sig string
		start, end          int
	}{
		{"o.ts/f", "Formats.", "export function f(a: unknown): string", 2, 4},
		{"o.ts/K.run", "Runs.", "run(a: unknown): void", 8, 9},
		{"o.ts/g", "", "declare function g(a: number): void;", 11, 12},
	} {
		s := syms[c.qualified]
		if docText(s) != c.doc || s.SigText != c.sig || s.Span.StartLine != c.start || s.Span.EndLine != c.end {
			t.Errorf("%s: doc %q sig %q span %+v", c.qualified, docText(s), s.SigText, s.Span)
		}
	}
	if syms["o.ts/f"].Visibility != extract.Public {
		t.Error("exported overloads are public")
	}
	if len(syms) != 4 {
		t.Errorf("got %v", syms)
	}
}

func TestAmbientAndNamespaces(t *testing.T) {
	src := "declare function g(): void;\nexport declare class D { m(): void; }\ndeclare const k: number;\n" +
		"export namespace NS {\n  export function h() {}\n  const z = 1;\n  namespace Inner { export let q = 1; }\n}\n" +
		"declare module \"x\" { export function y(): void; }\ndeclare module \"empty\";\n" +
		"declare module \"blank\" {}\n"
	syms := extractSrc(t, "typescript", "types.d.ts", src)
	for q, want := range map[string]struct {
		kind extract.Kind
		vis  extract.Visibility
		sig  string
	}{
		"types.d.ts/g":          {extract.KindFunction, extract.Package, "declare function g(): void;"},
		"types.d.ts/D":          {extract.KindClass, extract.Public, "export declare class D"},
		"types.d.ts/D.m":        {extract.KindMethod, extract.Public, "m(): void"},
		"types.d.ts/k":          {extract.KindConst, extract.Package, "declare const k: number;"},
		"types.d.ts/NS.h":       {extract.KindFunction, extract.Public, "export function h()"},
		"types.d.ts/NS.z":       {extract.KindConst, extract.Package, "const z = 1;"},
		"types.d.ts/NS.Inner.q": {extract.KindVar, extract.Package, "export let q = 1;"},
		"types.d.ts/x.y":        {extract.KindFunction, extract.Public, "export function y(): void;"},
	} {
		s, ok := syms[q]
		if !ok {
			t.Errorf("%s missing", q)
			continue
		}
		if s.Kind != want.kind || s.Visibility != want.vis || s.SigText != want.sig {
			t.Errorf("%s: kind %s vis %s sig %q", q, s.Kind, s.Visibility, s.SigText)
		}
	}
	if len(syms) != 8 {
		t.Errorf("got %v", syms)
	}
}

func TestMultiDeclaratorSignatures(t *testing.T) {
	src := "/** First. */\nlet a = 1, b = function () {};\nexport const c = 1, d = 2;\n"
	syms := extractSrc(t, "typescript", "m.ts", src)
	for q, want := range map[string]string{
		"m.ts/a": "let a = 1", "m.ts/b": "let b = function ()",
		"m.ts/c": "export const c = 1", "m.ts/d": "export const d = 2",
	} {
		if got := syms[q].SigText; got != want {
			t.Errorf("%s: sig %q, want %q", q, got, want)
		}
	}
	if docText(syms["m.ts/a"]) != "First." || syms["m.ts/b"].Doc != nil {
		t.Errorf("docs: a %q b %+v", docText(syms["m.ts/a"]), syms["m.ts/b"].Doc)
	}
}

func TestInterfaceMembersInSourceOrder(t *testing.T) {
	src := "interface I {\n  a: string;\n  m(): void;\n  m(x: number): void;\n  b: number;\n}\n"
	var got []string
	for _, s := range symbolsOf(t, "i.ts", src) {
		got = append(got, s.Qualified)
	}
	want := []string{"i.ts/I", "i.ts/I.a", "i.ts/I.m", "i.ts/I.b"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestTestCallVariantsAndDuplicates(t *testing.T) {
	src := "it.only('a', () => {});\ntest.skip('b', () => {});\ntest.each([1])('c %s', () => {});\n" +
		"it.each([1])('d', () => {});\nfoo.only('e', () => {});\nit.foo('f', () => {});\n" +
		"describe('outer', () => {\n  describe.skip('inner', () => { it('x', () => {}); });\n" +
		"  it('x', () => {});\n  it('x', () => {});\n  it('y', () => {});\n});\nit('x', () => {});\n"
	var got []string
	for _, s := range symbolsOf(t, "s.test.ts", src) {
		if !s.IsTest {
			t.Errorf("%s is not a test", s.Qualified)
		}
		got = append(got, s.Qualified)
	}
	want := []string{
		"s.test.ts/it:a", "s.test.ts/test:b", "s.test.ts/test:c %s", "s.test.ts/it:d",
		"s.test.ts/describe:outer/describe:inner/it:x",
		"s.test.ts/describe:outer/it:x", "s.test.ts/describe:outer/it:x~2",
		"s.test.ts/it:y", "s.test.ts/it:x",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

// TestQualifiedNamesUnique feeds inputs that repeat names in every way the
// extractor can see and requires unique qualified names, as the store does.
func TestQualifiedNamesUnique(t *testing.T) {
	src := "function f() {}\nfunction f() {}\nclass C { m() {} static m() {} x = 1; x = 2; }\n" +
		"class C {}\nvar v = 1;\nvar v = 2;\nnamespace N { export const a = 1; }\nnamespace N { export const a = 2; }\n" +
		"interface I { p: string; p: number; }\ndescribe('d', () => { it('t', () => {}); it('t', () => {}); });\n" +
		"it('t', () => {});\n"
	seen := map[string]bool{}
	for _, s := range symbolsOf(t, "u.test.ts", src) {
		if seen[s.Qualified] {
			t.Errorf("duplicate qualified name %s", s.Qualified)
		}
		seen[s.Qualified] = true
	}
}

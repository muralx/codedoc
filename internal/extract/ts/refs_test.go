package ts_test

import (
	"github.com/muralx/codedoc/internal/testutil"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/extract/ts"
)

const refSrc = `import D, * as NS from 'a';
import { x, y as z } from "b";
import './side-effect.js';
export * from './r.js';
export * as q from 'c';
export { m as n } from 'd';

export class C extends B<T> implements I, ns.J<U> {
  run(): void {
    this.items.get(new ns.Foo<X>(1), 2);
    helper();
    tag` + "`x`" + `;
  }
}

interface K extends L, M<N> { f: a.b.Typ }
`

func extractRefs(t *testing.T) *extract.FileResult {
	t.Helper()
	res, err := ts.New("typescript").Extract("src/c.ts", []byte(refSrc))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestReferences(t *testing.T) {
	res := extractRefs(t)
	const c, run, k = "src/c.ts/C", "src/c.ts/C.run", "src/c.ts/K"
	want := []extract.Reference{
		{Kind: extract.RefExtend, Name: "B", Enclosing: c, Line: 8, Arity: -1},
		{Kind: extract.RefType, Name: "T", Enclosing: c, Line: 8, Arity: -1},
		{Kind: extract.RefImplement, Name: "I", Enclosing: c, Line: 8, Arity: -1},
		{Kind: extract.RefImplement, Name: "J", Qualifier: "ns", Enclosing: c, Line: 8, Arity: -1},
		{Kind: extract.RefType, Name: "U", Enclosing: c, Line: 8, Arity: -1},
		{Kind: extract.RefCall, Name: "get", Qualifier: "this.items", Enclosing: run, Line: 10, Arity: 2},
		{Kind: extract.RefInstantiate, Name: "Foo", Qualifier: "ns", Enclosing: run, Line: 10, Arity: 1},
		{Kind: extract.RefType, Name: "X", Enclosing: run, Line: 10, Arity: -1},
		{Kind: extract.RefCall, Name: "helper", Enclosing: run, Line: 11, Arity: 0},
		{Kind: extract.RefCall, Name: "tag", Enclosing: run, Line: 12, Arity: 1},
		{Kind: extract.RefExtend, Name: "L", Enclosing: k, Line: 16, Arity: -1},
		{Kind: extract.RefExtend, Name: "M", Enclosing: k, Line: 16, Arity: -1},
		{Kind: extract.RefType, Name: "N", Enclosing: k, Line: 16, Arity: -1},
		{Kind: extract.RefType, Name: "Typ", Qualifier: "a.b", Enclosing: k + ".f", Line: 16, Arity: -1},
	}
	if !reflect.DeepEqual(testutil.WithoutCols(res.References), want) {
		t.Errorf("got\n%+v\nwant\n%+v", res.References, want)
	}
}

func TestImports(t *testing.T) {
	want := []extract.Import{
		{Raw: "a", Alias: "NS", Line: 1},
		{Raw: "b", Names: []string{"x", "y=z"}, Line: 2},
		{Raw: "./side-effect.js", Line: 3},
		{Raw: "./r.js", Line: 4, Reexport: true},
		{Raw: "c", Alias: "q", Line: 5, Reexport: true},
		{Raw: "d", Names: []string{"m=n"}, Line: 6, Reexport: true},
	}
	if got := extractRefs(t).Imports; !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

func TestQualifierIsTrailingChain(t *testing.T) {
	long := strings.Repeat("abcdefghij.", 20) + "end"
	src := "function g() {\n" +
		"  x.Do(1).Then();\n" +
		"  (() => { return 1; }).call();\n" +
		"  this.items.add();\n" +
		"  xs[0].f();\n" +
		"  " + long + ".z();\n" +
		"}\n"
	res, err := ts.New("typescript").Extract("src/q.ts", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	quals := map[string]string{}
	for _, r := range res.References {
		if r.Kind == extract.RefCall {
			quals[r.Name] = r.Qualifier
		}
	}
	for name, want := range map[string]string{"Then": "x.Do()", "call": "()", "add": "this.items", "f": "()"} {
		if quals[name] != want {
			t.Errorf("qualifier of %s = %q, want %q", name, quals[name], want)
		}
	}
	if z := quals["z"]; len(z) > 120 || !strings.HasSuffix(z, ".end") || !strings.HasPrefix(z, "abcdefghij.") {
		t.Errorf("long qualifier not capped at a segment boundary: %q", z)
	}
}

func TestRequireAndDynamicImport(t *testing.T) {
	src := "const fs = require('fs');\n" +
		"const { a } = require(\"./lib\");\n" +
		"async function f() { await import('./dyn.js'); require(name); }\n"
	res, err := ts.New("javascript").Extract("src/r.js", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	want := []extract.Import{
		{Raw: "fs", Alias: "fs", Line: 1},
		{Raw: "./lib", Line: 2},
		{Raw: "./dyn.js", Line: 3},
	}
	if !reflect.DeepEqual(res.Imports, want) {
		t.Errorf("imports %+v", res.Imports)
	}
	for _, r := range res.References {
		if r.Name == "require" && r.Line != 3 {
			t.Errorf("a literal require is an import, not a call: %+v", r)
		}
	}
}

func TestBuiltinGlobalsAreNotReferences(t *testing.T) {
	src := "let m: Map<string, Array<Foo>> = new Map();\n" +
		"let p: Partial<Record<string, Promise<Bar>>>;\n" +
		"let u: Uint8Array | ReturnType<typeof f> | Awaited<NonNullable<Date>>;\n" +
		"class E extends Error {}\n" +
		"const s = new Set([1, 2]); const r = new RegExp('x'); new Baz(1, 2); new Qux;\n"
	res, err := ts.New("typescript").Extract("src/b.ts", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	arity := map[string]int{}
	for _, r := range res.References {
		got = append(got, r.Name)
		arity[r.Name] = r.Arity
	}
	if want := []string{"Foo", "Bar", "Baz", "Qux"}; !reflect.DeepEqual(got, want) {
		t.Errorf("references %v, want %v", got, want)
	}
	if arity["Baz"] != 2 || arity["Qux"] != 0 {
		t.Errorf("new arity %v", arity)
	}
}

func TestConfigCandidates(t *testing.T) {
	src := "export const f = () => {\n" +
		"  const a = process.env.API_URL;\n" +
		"  const b = process.env['LIB_MODE'];\n" +
		"  const c = other.env.NOPE + process.env[k];\n" +
		"  load(\"app.mode\", 'not a key', `TEMPLATE_KEY`, `${x}`);\n" +
		"  return new Config('DB_HOST');\n" +
		"};\n"
	res, err := ts.New("typescript").Extract("src/f.ts", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range res.References {
		if r.Kind == extract.RefConfig {
			if r.Enclosing != "src/f.ts/f" || r.Arity != -1 {
				t.Errorf("config ref %+v", r)
			}
			got = append(got, r.Name+"@"+strconv.Itoa(r.Line)+":"+r.Qualifier)
		}
	}
	want := []string{"API_URL@2:accessor", "LIB_MODE@3:accessor", "k@4:accessor-const", "app.mode@5:literal",
		"TEMPLATE_KEY@5:literal", "DB_HOST@6:literal"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("config refs %v, want %v", got, want)
	}
}

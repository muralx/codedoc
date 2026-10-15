package ts_test

import (
	"testing"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/extract/golden"
	"github.com/muralx/codedoc/internal/extract/ts"
	"github.com/muralx/codedoc/internal/parse"
)

// byPath picks the extractor for each file's language, as the pipeline does.
type byPath struct{}

func (byPath) Extract(path string, src []byte) (*extract.FileResult, error) {
	e, _ := extract.For(parse.LanguageForPath(path))
	return e.Extract(path, src)
}

func TestGolden(t *testing.T) {
	golden.RunFixtures(t, byPath{}, golden.Sources(t, "ts-", ".ts", ".tsx", ".js", ".jsx"))
}

func TestRegistered(t *testing.T) {
	for _, lang := range []string{"typescript", "tsx", "javascript"} {
		if _, ok := extract.For(lang); !ok {
			t.Errorf("%s not registered", lang)
		}
	}
}

func extractSrc(t *testing.T, lang, path, src string) map[string]extract.Symbol {
	t.Helper()
	res, err := ts.New(lang).Extract(path, []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]extract.Symbol{}
	for _, s := range res.Symbols {
		m[s.Qualified] = s
	}
	return m
}

const edgeCases = `/** Not a module doc: this file is not an index. */

// Plain comment, not a doc.
function plain() {}

/** Doubles. */
export const double = (n: number): number =>
  n * 2;

let counter = 0, other = function () { return 1; };

const [destructured] = [1];

/** Shapes. */
export enum Shape { Circle, Square }

export default class Base<T> extends Object {
  #secret = 1;
  protected guarded = 2;
  static readonly count: number = 0;
  constructor(private readonly v: T) { super(); }
  /** Overloaded. */
  over(a: string): void;
  over(a: number): void;
  over(a: unknown): void {}
}

abstract class Shape2 {
  abstract area(): number;
  private hidden(): void {}
}

interface Opts {
  'quoted-key'?: string;
  run(): void;
}
`

func TestEdgeCases(t *testing.T) {
	syms := extractSrc(t, "typescript", "lib/edge.ts", edgeCases)
	for _, c := range []struct {
		qualified, kind, doc, sig string
		vis                       extract.Visibility
	}{
		{"lib/edge.ts/plain", "function", "", "function plain()", extract.Package},
		{"lib/edge.ts/double", "function", "Doubles.", "export const double = (n: number): number =>", extract.Public},
		{"lib/edge.ts/counter", "var", "", "let counter = 0", extract.Package},
		{"lib/edge.ts/other", "function", "", "let other = function ()", extract.Package},
		{"lib/edge.ts/Shape", "enum", "Shapes.", "export enum Shape", extract.Public},
		{"lib/edge.ts/Base", "class", "", "export default class Base<T> extends Object", extract.Public},
		{"lib/edge.ts/Base.#secret", "field", "", "#secret = 1", extract.Private},
		{"lib/edge.ts/Base.guarded", "field", "", "protected guarded = 2", extract.Package},
		{"lib/edge.ts/Base.count", "field", "", "static readonly count: number = 0", extract.Public},
		{"lib/edge.ts/Base.constructor", "method", "", "constructor(private readonly v: T)", extract.Public},
		{"lib/edge.ts/Base.over", "method", "Overloaded.", "over(a: unknown): void", extract.Public},
		{"lib/edge.ts/Shape2", "class", "", "abstract class Shape2", extract.Package},
		{"lib/edge.ts/Shape2.area", "method", "", "abstract area(): number", extract.Package},
		{"lib/edge.ts/Shape2.hidden", "method", "", "private hidden(): void", extract.Private},
		{"lib/edge.ts/Opts", "interface", "", "interface Opts", extract.Package},
		{"lib/edge.ts/Opts.quoted-key", "field", "", "'quoted-key'?: string", extract.Package},
		{"lib/edge.ts/Opts.run", "method", "", "run(): void", extract.Package},
	} {
		s, ok := syms[c.qualified]
		if !ok {
			t.Errorf("%s: missing", c.qualified)
			continue
		}
		doc := ""
		if s.Doc != nil {
			doc = s.Doc.Text
		}
		if string(s.Kind) != c.kind || doc != c.doc || s.SigText != c.sig || s.Visibility != c.vis {
			t.Errorf("%s: got kind %s doc %q sig %q vis %s", c.qualified, s.Kind, doc, s.SigText, s.Visibility)
		}
	}
	if len(syms) != 17 {
		t.Errorf("got %d symbols: %v", len(syms), syms)
	}
}

func TestModuleDocOnlyInIndex(t *testing.T) {
	src := "/**\n * The domain package.\n *\n * Second paragraph.\n */\n\nexport * from './a.js';\n"
	res, err := ts.New("typescript").Extract("src/index.ts", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if res.ModuleDoc == nil || res.ModuleDoc.Text != "The domain package.\n\nSecond paragraph." {
		t.Fatalf("module doc %+v", res.ModuleDoc)
	}
	res, err = ts.New("typescript").Extract("src/other.ts", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if res.ModuleDoc != nil {
		t.Fatal("module doc only comes from index files")
	}
}

func TestTestCallsAndJSX(t *testing.T) {
	src := "describe('repo', () => {\n  test(`loads: ok`, () => {});\n  it(name, () => {});\n});\nfoo('x');\n" +
		"const view = () => <div className=\"x\"/>;\n"
	syms := extractSrc(t, "javascript", "src/__tests__/repo.jsx", src)
	s, ok := syms["src/__tests__/repo.jsx/test:loads: ok"]
	if !ok || !s.IsTest || s.Name != "loads: ok" || s.Kind != extract.KindFunction {
		t.Fatalf("test symbol: %+v in %v", s, syms)
	}
	if v := syms["src/__tests__/repo.jsx/view"]; v.IsTest || v.Kind != extract.KindFunction {
		t.Errorf("view: %+v", v)
	}
	if len(syms) != 2 {
		t.Errorf("got %v", syms)
	}
	if len(extractSrc(t, "typescript", "src/repo.ts", "it('x', () => {});\n")) != 0 {
		t.Error("it() outside test files is not a test")
	}
}

func TestBodyHashCoversSignature(t *testing.T) {
	a := extractSrc(t, "typescript", "a.ts", "export function f(x: number) { return 1; }\n")["a.ts/f"]
	b := extractSrc(t, "typescript", "a.ts", "export function f(y: number) { return 1; }\n")["a.ts/f"]
	if a.BodyHash == "" || a.BodyHash == b.BodyHash {
		t.Errorf("hashes %q %q", a.BodyHash, b.BodyHash)
	}
}

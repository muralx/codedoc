package write

import "testing"

func TestTSSymbolDocs(t *testing.T) {
	src := "import { a } from './a';\n\nexport function f(x: number): number {\n  return x;\n}\n\nexport class C {\n  /** Old. */\n  @dec()\n  m(): void {}\n}\n"
	files := map[string]string{"pkg/src/f.ts": src}
	tg := func(q string) Target {
		return Target{Path: "pkg/src/f.ts", ExtractPath: "src/f.ts", Lang: "typescript", Qualified: q}
	}
	runRoundTrips(t, []roundTrip{
		{name: "create above export", files: files, tg: tg("src/f.ts/f"), text: "Returns x.", file: "pkg/src/f.ts",
			want: "import { a } from './a';\n\n/** Returns x. */\nexport function f(x: number): number {\n" + src[len("import { a } from './a';\n\nexport function f(x: number): number {\n"):],
			doc:  "Returns x."},
		{name: "replace member, multi-paragraph", files: files, tg: tg("src/f.ts/C.m"), text: "Runs m.\n\nTwice.", file: "pkg/src/f.ts",
			want: src[:len(src)-len("  /** Old. */\n  @dec()\n  m(): void {}\n}\n")] + "  /**\n   * Runs m.\n   *\n   * Twice.\n   */\n  @dec()\n  m(): void {}\n}\n",
			doc:  "Runs m.\n\nTwice."},
	})
	jsFiles := map[string]string{"a.js": "const g = () => 1;\n"}
	runRoundTrips(t, []roundTrip{
		{name: "javascript", files: jsFiles, tg: Target{Path: "a.js", Lang: "javascript", Qualified: "a.js/g"}, text: "G.", file: "a.js",
			want: "/** G. */\nconst g = () => 1;\n", doc: "G."},
	})
}

func TestTSRejectsCommentEnd(t *testing.T) {
	root := repo(t, map[string]string{"a.ts": "function f() {}\n"})
	if _, err := Plan(Target{Root: root, Path: "a.ts", Lang: "typescript", Qualified: "a.ts/f"}, "a */ b"); err == nil {
		t.Fatal("*/ accepted")
	}
}

func TestTSModuleDocs(t *testing.T) {
	tg := Target{Path: "src", Lang: "typescript", ModuleDir: "src"}
	runRoundTrips(t, []roundTrip{
		{name: "create README", files: map[string]string{"src/index.ts": ""}, tg: tg, text: "Domain types.", file: "src/README.md",
			want: "## Purpose\n\nDomain types.\n", doc: "Domain types."},
		{name: "append section", files: map[string]string{"src/README.md": "# Src\n\nIntro."}, tg: tg, text: "Domain *types*.\n\n- a\n- b", file: "src/README.md",
			want: "# Src\n\nIntro.\n\n## Purpose\n\nDomain *types*.\n\n- a\n- b\n", doc: "Domain *types*.\n\n- a\n- b"},
		{name: "replace section", files: map[string]string{"src/README.md": "# Src\n\n## Purpose\nold\n```\n## not a heading\n```\n### Sub\n\n## Usage\n\nuse it\n"},
			tg: tg, text: "New.", file: "src/README.md",
			want: "# Src\n\n## Purpose\n\nNew.\n\n## Usage\n\nuse it\n", doc: "New."},
		{name: "heading at end", files: map[string]string{"src/README.md": "## purpose"}, tg: tg, text: "New.", file: "src/README.md",
			want: "## purpose\n\nNew.\n", doc: "New."},
		{name: "empty README", files: map[string]string{"src/README.md": ""}, tg: tg, text: "New.", file: "src/README.md",
			want: "## Purpose\n\nNew.\n", doc: "New."},
	})
}
